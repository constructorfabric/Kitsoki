// Package main — record.go implements `kitsoki record`.
//
// # kitsoki record — flow-driven GIF recording
//
// kitsoki record replays a deterministic flow through the state machine, renders
// each state's view as a text frame, rasterises the frames to images, and
// encodes them as an animated GIF.  The result is deterministic: the same
// flow YAML + the same flags always produce the same GIF bytes.
//
// This replaces the VHS-based pipeline that required:
//
//   - A hand-authored .tape file (inputs + timing hardcoded)
//   - A matching recording.yaml that duplicated the tape's inputs
//   - VHS + headless ttyd + ffmpeg installed and working
//   - Font rendering that varied by machine / container image
//
// With kitsoki record, the flow YAML is the single source of truth.  The same
// file drives both `kitsoki test flows` (correctness) and `kitsoki record`
// (demo animation).  No external tools, no font rendering variance.
//
// # Rasterisation approach
//
// Path B (minimal deps): golang.org/x/image/font/basicfont draws monospace
// text cells into a 2-D RGBA canvas.  No ANSI colour support; ANSI escapes
// in view templates are stripped before rendering.  The output is readable
// monospace text on a dark/light background — not a pixel-perfect terminal
// screenshot.  If richer rendering is wanted in the future, swap
// rasteriseFrame for a termshot/termview integration (Path A).
//
// # Usage
//
//	kitsoki record app.yaml --flow flows/main.yaml -o out.gif
//	kitsoki record app.yaml --flow flows/         # all flows in dir
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// ─── theme ────────────────────────────────────────────────────────────────────

// recordTheme holds background and foreground colours for rasterisation.
type recordTheme struct {
	bg color.RGBA
	fg color.RGBA
}

var recordThemes = map[string]recordTheme{
	"molokai": {
		bg: color.RGBA{R: 28, G: 28, B: 28, A: 255},
		fg: color.RGBA{R: 248, G: 248, B: 242, A: 255},
	},
	"dracula": {
		bg: color.RGBA{R: 40, G: 42, B: 54, A: 255},
		fg: color.RGBA{R: 248, G: 248, B: 242, A: 255},
	},
	"light": {
		bg: color.RGBA{R: 253, G: 253, B: 253, A: 255},
		fg: color.RGBA{R: 30, G: 30, B: 30, A: 255},
	},
}

// ─── recordCmd ───────────────────────────────────────────────────────────────

func recordCmd() *cobra.Command {
	var (
		flowPath      string
		outPath       string
		widthPx       int
		heightPx      int
		themeName     string
		frameMS       int
		settleMS      int
		recordingPath string
	)

	cmd := &cobra.Command{
		Use:   "record <app.yaml>",
		Short: "Replay a flow and encode the state views as an animated GIF",
		Long: `Replay a deterministic flow through the app's state machine,
render each state's view as a text frame, and encode the sequence as an
animated GIF.

The same flow YAML that drives 'kitsoki test flows' also drives 'kitsoki record'.
No VHS, no headless browser, no font rendering variance.

Rasterisation uses basicfont from golang.org/x/image (Path B — no heavy deps).
Each frame is a fixed-size image filled with the theme background, with the
view text drawn in the theme foreground colour.  ANSI escapes in view
templates are stripped before rendering.

Exit codes:
  0  success
  1  flow error (validation, recording miss, etc.)
  2  I/O error (bad paths, write failure)

Examples:
  kitsoki record testdata/apps/cloak/app.yaml --flow testdata/apps/cloak/flows/winning.yaml
  kitsoki record app.yaml --flow flows/ -o demo.gif --theme dracula
  kitsoki record app.yaml --flow flows/main.yaml --recording recording.yaml --frame-ms 3000`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			th, ok := recordThemes[themeName]
			if !ok {
				return fmt.Errorf("unknown theme %q (use molokai|dracula|light)", themeName)
			}

			// Collect flow files.
			flowFiles, err := resolveFlowPaths(flowPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "kitsoki record: resolve flows: %v\n", err)
				os.Exit(2)
			}

			// Default output path: first flow basename + .gif.
			if outPath == "" {
				base := filepath.Base(flowFiles[0])
				outPath = strings.TrimSuffix(base, filepath.Ext(base)) + ".gif"
			}

			cfg := recordConfig{
				appPath:       appPath,
				flowFiles:     flowFiles,
				outPath:       outPath,
				width:         widthPx,
				height:        heightPx,
				theme:         th,
				frameDelay:    centiseconds(frameMS),
				settleDelay:   centiseconds(settleMS),
				recordingPath: recordingPath,
			}

			if err := runRecord(cfg); err != nil {
				// Distinguish I/O errors (exit 2) from flow errors (exit 1).
				if isRecordIOError(err) {
					fmt.Fprintf(os.Stderr, "kitsoki record: I/O error: %v\n", err)
					os.Exit(2)
				}
				fmt.Fprintf(os.Stderr, "kitsoki record: %v\n", err)
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flowPath, "flow", "", "flow YAML file or directory of flow files (required)")
	_ = cmd.MarkFlagRequired("flow")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output GIF path (default: <flow-basename>.gif)")
	cmd.Flags().IntVar(&widthPx, "width", 2560, "frame width in pixels")
	cmd.Flags().IntVar(&heightPx, "height", 1800, "frame height in pixels")
	cmd.Flags().StringVar(&themeName, "theme", "molokai", "colour theme: molokai|dracula|light")
	cmd.Flags().IntVar(&frameMS, "frame-ms", 2500, "how long each frame shows (milliseconds)")
	cmd.Flags().IntVar(&settleMS, "settle-ms", 1500, "brief pause after each frame (milliseconds)")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "recording YAML for input: turns in the flow")

	return cmd
}

// centiseconds converts milliseconds to GIF delay units (100ths of a second).
func centiseconds(ms int) int {
	cs := ms / 10
	if cs < 1 {
		cs = 1
	}
	return cs
}

// isRecordIOError is a heuristic to classify I/O errors by message prefix.
func isRecordIOError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "write ") ||
		strings.Contains(s, "create ") ||
		strings.Contains(s, "mkdir ") ||
		strings.Contains(s, "encode GIF")
}

// resolveFlowPaths returns flow YAML file paths from a file path or directory.
func resolveFlowPaths(flowPath string) ([]string, error) {
	if flowPath == "" {
		return nil, fmt.Errorf("--flow is required")
	}
	info, err := os.Stat(flowPath)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", flowPath, err)
	}
	if !info.IsDir() {
		return []string{flowPath}, nil
	}
	// Directory: collect *.yaml / *.yml files.
	entries, err := os.ReadDir(flowPath)
	if err != nil {
		return nil, fmt.Errorf("read dir %q: %w", flowPath, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml") {
			files = append(files, filepath.Join(flowPath, e.Name()))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML files found in directory %q", flowPath)
	}
	return files, nil
}

// ─── record pipeline ─────────────────────────────────────────────────────────

type recordConfig struct {
	appPath       string
	flowFiles     []string
	outPath       string
	width         int
	height        int
	theme         recordTheme
	frameDelay    int // centiseconds (GIF delay units)
	settleDelay   int // centiseconds
	recordingPath string
}

// runRecord executes the full pipeline: load app → replay flows →
// collect text frames → rasterise → encode GIF → write to disk.
func runRecord(cfg recordConfig) error {
	// 1. Load app. loadAppWithEnv publishes KITSOKI_APP_DIR before
	// Load so env-expanded fields (cwd:) validate against the live
	// var rather than tripping the loader's missing-var check.
	def, err := loadAppWithEnv(cfg.appPath)
	if err != nil {
		return err
	}

	// 2. Build machine.
	m, err := machine.New(def)
	if err != nil {
		return fmt.Errorf("build machine: %w", err)
	}

	// 3. Replay each flow file and collect text frames.
	var frames []string
	for _, flowFile := range cfg.flowFiles {
		ff, err := replayFlowToFrames(def, m, flowFile, cfg.recordingPath)
		if err != nil {
			return fmt.Errorf("replay flow %q: %w", flowFile, err)
		}
		frames = append(frames, ff...)
	}
	if len(frames) == 0 {
		return fmt.Errorf("no frames produced from %d flow file(s)", len(cfg.flowFiles))
	}

	// 4. Rasterise + encode.
	anim, err := framesToGIF(frames, cfg)
	if err != nil {
		return fmt.Errorf("rasterise frames: %w", err)
	}

	// 5. Write GIF.
	f, err := os.Create(cfg.outPath)
	if err != nil {
		return fmt.Errorf("create %q: %w", cfg.outPath, err)
	}
	defer func() { _ = f.Close() }()

	if err := gif.EncodeAll(f, anim); err != nil {
		return fmt.Errorf("encode GIF %q: %w", cfg.outPath, err)
	}

	fmt.Printf("kitsoki record: wrote %d frames → %s\n", len(anim.Image), cfg.outPath)
	return nil
}

// ─── flow replay → text frames ───────────────────────────────────────────────

// recordFlowFixture mirrors testrunner.FlowFixture but is defined here to avoid
// an import cycle (testrunner imports machine; cmd imports testrunner already).
// Only the fields we need for recording are decoded.
type recordFlowFixture struct {
	TestKind     string           `yaml:"test_kind"`
	Recording    string           `yaml:"recording,omitempty"`
	InitialState string           `yaml:"initial_state"`
	InitialWorld map[string]any   `yaml:"initial_world,omitempty"`
	Turns        []recordFlowTurn `yaml:"turns"`
}

type recordFlowTurn struct {
	Intent *recordFlowIntent `yaml:"intent,omitempty"`
	Input  string            `yaml:"input,omitempty"`
}

type recordFlowIntent struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots,omitempty"`
}

// replayFlowToFrames loads a flow YAML file, parses all documents, and replays
// each flow fixture through the machine, collecting one text frame per turn.
func replayFlowToFrames(def *app.AppDef, m machine.Machine, flowFile, recordingPath string) ([]string, error) {
	data, err := os.ReadFile(flowFile)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", flowFile, err)
	}

	docs := strings.Split(string(data), "\n---")

	var allFrames []string
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var fixture recordFlowFixture
		if err := yaml.Unmarshal([]byte(doc), &fixture); err != nil {
			return nil, fmt.Errorf("parse fixture in %q: %w", flowFile, err)
		}
		if fixture.TestKind != "flow" {
			continue
		}

		frames, err := replayOneRecordFixture(def, m, &fixture, flowFile, recordingPath)
		if err != nil {
			return nil, err
		}
		allFrames = append(allFrames, frames...)
	}
	return allFrames, nil
}

// replayOneRecordFixture replays a single fixture and returns text frames.
func replayOneRecordFixture(def *app.AppDef, m machine.Machine, fixture *recordFlowFixture, flowFile, recordingPath string) ([]string, error) {
	// Load recording if needed.
	var replayH *harness.ReplayHarness
	effectiveRecording := recordingPath
	if effectiveRecording == "" {
		effectiveRecording = fixture.Recording
	}
	if effectiveRecording != "" {
		if !filepath.IsAbs(effectiveRecording) {
			effectiveRecording = filepath.Join(filepath.Dir(flowFile), effectiveRecording)
		}
		rh, err := harness.NewReplay(effectiveRecording)
		if err != nil {
			return nil, fmt.Errorf("load recording %q: %w", effectiveRecording, err)
		}
		replayH = rh
	}

	// Initialise world.
	currentWorld := machine.WorldFromSchema(def.World)
	for k, v := range fixture.InitialWorld {
		currentWorld.Vars[k] = v
	}
	currentState := app.StatePath(fixture.InitialState)

	var frames []string

	// First frame: initial state view (before any turn).
	initialView, _ := m.RenderState(currentState, currentWorld)
	frames = append(frames, composeFrame(string(currentState), initialView, ""))

	ctx := context.Background()

	for i, turn := range fixture.Turns {
		var call intent.IntentCall

		switch {
		case turn.Intent != nil:
			call = intent.IntentCall{
				Intent: turn.Intent.Name,
				Slots:  world.Slots(turn.Intent.Slots),
			}

		case turn.Input != "":
			if replayH == nil {
				return nil, fmt.Errorf("turn %d: input %q requires a recording but none loaded", i+1, turn.Input)
			}
			params, err := replayH.RunTurn(ctx, harness.TurnInput{
				StatePath: currentState,
				UserText:  turn.Input,
			})
			if err != nil {
				return nil, fmt.Errorf("turn %d: recording miss for %q in state %q: %w", i+1, turn.Input, currentState, err)
			}
			c, err := recordParamsToIntentCall(params)
			if err != nil {
				return nil, fmt.Errorf("turn %d: parse recording: %w", i+1, err)
			}
			call = c

		default:
			return nil, fmt.Errorf("turn %d: neither intent nor input is set", i+1)
		}

		result, err := m.Turn(ctx, currentState, currentWorld, call)
		if err != nil {
			return nil, fmt.Errorf("turn %d: machine.Turn: %w", i+1, err)
		}

		if result.ValidationError != nil {
			// Show the rejection as a frame; state unchanged.
			frames = append(frames, composeFrame(
				string(currentState),
				"[rejected: "+result.ValidationError.Error()+"]",
				call.Intent,
			))
			continue
		}

		currentState = result.NewState
		currentWorld = result.World

		// Use the transition view if set; otherwise fall back to state view.
		frameText := result.View
		if frameText == "" {
			frameText, _ = m.RenderState(currentState, currentWorld)
		}
		frames = append(frames, composeFrame(string(currentState), frameText, call.Intent))
	}

	return frames, nil
}

// composeFrame assembles a displayable text block from state path + view text.
func composeFrame(state, view, intentName string) string {
	var sb strings.Builder
	if intentName != "" {
		sb.WriteString("[ ")
		sb.WriteString(intentName)
		sb.WriteString(" ]\n\n")
	}
	sb.WriteString("── ")
	sb.WriteString(state)
	sb.WriteString(" ──\n\n")
	sb.WriteString(stripANSI(view))
	return sb.String()
}

// recordParamsToIntentCall converts mcp.CallToolParams to an IntentCall.
// Mirrors testrunner.paramsToIntentCall without the import cycle.
func recordParamsToIntentCall(params mcp.CallToolParams) (intent.IntentCall, error) {
	if params.Name != "transition" {
		return intent.IntentCall{}, fmt.Errorf("unexpected tool name %q (want transition)", params.Name)
	}
	argsMap, ok := params.Arguments.(map[string]any)
	if !ok {
		b, _ := json.Marshal(params.Arguments)
		if err := json.Unmarshal(b, &argsMap); err != nil {
			return intent.IntentCall{}, fmt.Errorf("parse arguments: %w", err)
		}
	}
	intentName, _ := argsMap["intent"].(string)
	if intentName == "" {
		return intent.IntentCall{}, fmt.Errorf("missing intent field")
	}
	var slots world.Slots
	if sv, ok := argsMap["slots"]; ok && sv != nil {
		if sm, ok := sv.(map[string]any); ok {
			slots = world.Slots(sm)
		}
	}
	return intent.IntentCall{Intent: intentName, Slots: slots}, nil
}

// ─── rasterisation ────────────────────────────────────────────────────────────

// framesToGIF rasterises text frames into an *gif.GIF ready for encoding.
func framesToGIF(frames []string, cfg recordConfig) (*gif.GIF, error) {
	anim := &gif.GIF{LoopCount: 0} // 0 = infinite loop

	pal := makeRecordPalette(cfg.theme)

	for i, frame := range frames {
		img := rasteriseFrame(frame, cfg.width, cfg.height, cfg.theme)
		palImg := rgbaToPaletted(img, pal)
		anim.Image = append(anim.Image, palImg)

		// Last frame gets extra delay (frameDelay + settleDelay).
		delay := cfg.frameDelay
		if i == len(frames)-1 {
			delay = cfg.frameDelay + cfg.settleDelay
		}
		anim.Delay = append(anim.Delay, delay)
		anim.Disposal = append(anim.Disposal, 0x02) // restore to background
	}

	return anim, nil
}

// rasteriseFrame renders text into an RGBA image using basicfont.Face7x13.
//
// basicfont.Face7x13 is a 7×13 pixel fixed-size face.  A scale factor of 2
// doubles each cell to 14×26 px, which produces legible output at 2560×1800.
// Characters outside the ASCII 0x20–0x7e range are drawn as spaces.
func rasteriseFrame(text string, w, h int, th recordTheme) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	// Fill background.
	bgUniform := image.NewUniform(th.bg)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, th.bg)
		}
	}
	_ = bgUniform

	// basicfont.Face7x13: 7px wide, 13px tall (advance).
	// We scale by 2 so each character occupies a 14×26 block.
	const baseW = 7
	const baseH = 13
	const scale = 2
	cellW := baseW * scale
	cellH := baseH * scale

	const marginX = 48
	const marginY = 48

	lines := strings.Split(text, "\n")

	drawer := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(th.fg),
		Face: basicfont.Face7x13,
	}

	for lineIdx, line := range lines {
		// Baseline of this line.
		baseline := marginY + lineIdx*cellH + cellH

		if baseline > h-marginY {
			break // ran off the bottom
		}

		x := marginX
		for _, r := range line {
			if x+cellW > w-marginX {
				break // ran off the right edge
			}
			// Replace non-printable / non-ASCII with space for basicfont.
			if r < 0x20 || r > 0x7e {
				r = ' '
			}
			// Scale: draw the character at 2× by printing it at (x, baseline)
			// with a scaled drawer.  basicfont doesn't support fractional coords
			// so we use the face directly and rely on the 2× cellW stride.
			drawer.Dot = fixed.P(x, baseline)
			drawer.DrawString(string(r))
			x += cellW
		}
	}

	return img
}

// makeRecordPalette builds a GIF colour palette that includes the theme
// bg/fg colours plus the standard Plan9 256-colour palette for content.
func makeRecordPalette(th recordTheme) color.Palette {
	pal := make(color.Palette, len(palette.Plan9))
	copy(pal, palette.Plan9)
	// Pin theme colours to indices 0 and 1 so they are always reachable.
	pal[0] = th.bg
	pal[1] = th.fg
	return pal
}

// rgbaToPaletted converts an RGBA image to a Paletted image via nearest-colour
// matching.  This is the bottleneck for large frames; it's O(w×h) but stdlib.
func rgbaToPaletted(src *image.RGBA, pal color.Palette) *image.Paletted {
	bounds := src.Bounds()
	dst := image.NewPaletted(bounds, pal)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(x, y, pal.Convert(src.At(x, y)))
		}
	}
	return dst
}

// ─── ANSI stripping ───────────────────────────────────────────────────────────

// stripANSI removes ANSI CSI escape sequences (ESC [ ... final-byte) and
// two-character ESC sequences from a string.
func stripANSI(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) {
			if s[i+1] == '[' {
				// CSI sequence: ESC [ <params> <final-byte 0x40–0x7e>
				i += 2
				for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
					i++
				}
				if i < len(s) {
					i++ // consume final byte
				}
				continue
			}
			// Two-character escape (e.g. ESC M).
			i += 2
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
