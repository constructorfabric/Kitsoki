/**
 * The shared "camera" — one device-profile registry every recording spec records
 * through, so the per-section MP4s compose into the master tour without any of
 * them letterboxing or shifting scale.
 *
 * BEFORE this module each spec spelled out its own `viewport` /
 * `deviceScaleFactor` / `recordVideo.size` inline, and they had drifted
 * (1600×900 mostly, but 1440×900 in a few, deviceScaleFactor present in some and
 * absent in others). That drift is invisible per-spec but fatal to the stitch:
 * `concat-videos.sh` composes at a single canvas size (1600×900), so any section
 * recorded at another size would be pillar/letter-boxed in the master. Routing
 * every `newContext` through {@link cameraContext} makes the dimension a single
 * sourced fact, and `pnpm demos:lint` fails any demo spec that bypasses it.
 *
 * The profile is also the seam for the device matrix (Part B): a spec reads
 * whatever {@link activeProfile} returns, and `KITSOKI_DEMO_PROFILE` selects it
 * per recording pass — desktop today, `mobile`/`tablet` opt-in per demo once the
 * SPA has the breakpoints to make a narrow cut more than a shrunken desktop.
 */

/** A named recording device profile. The `id` is also the artifact-path suffix. */
export interface CameraProfile {
  readonly id: string;
  readonly width: number;
  readonly height: number;
  /** Backing-store scale. 2 keeps room text / posters crisp; video stays at
   *  `width`×`height` because `recordVideo.size` is set explicitly. */
  readonly deviceScaleFactor: number;
  readonly fps: number;
  /** Emulate a touch device (sets Playwright `hasTouch` + `isMobile`). */
  readonly touch: boolean;
  /** Port offset added to every spec's base port so parallel profile passes of
   *  one spec never collide (see demoAddr in server.ts). */
  readonly portShift: number;
}

/**
 * The device matrix. `desktop` is the canonical default and the ONLY profile
 * enabled per demo until that demo's UI is responsive — see the Part B honesty
 * gate. `tablet`/`mobile` exist so the matrix infra is real now; flipping a demo
 * onto them is a deliberate per-demo opt-in, not a default.
 *
 * desktop's 1600×900 is the canvas `concat-videos.sh` composes at — keep them in
 * lockstep or the master tour letterboxes.
 */
export const PROFILES = {
  desktop: { id: "desktop", width: 1600, height: 900, deviceScaleFactor: 2, fps: 30, touch: false, portShift: 0 },
  tablet: { id: "tablet", width: 1112, height: 834, deviceScaleFactor: 2, fps: 30, touch: true, portShift: 1000 },
  mobile: { id: "mobile", width: 390, height: 844, deviceScaleFactor: 3, fps: 30, touch: true, portShift: 2000 },
} as const satisfies Record<string, CameraProfile>;

export type ProfileId = keyof typeof PROFILES;

/** The brand background the stitch compositor / title cards paint behind video. */
export const BRAND_BG = "#070d1a";

/** The env var that selects the recording profile for a pass (Part B). */
export const PROFILE_ENV = "KITSOKI_DEMO_PROFILE";

/**
 * The profile this recording pass runs under, from `KITSOKI_DEMO_PROFILE`
 * (default `desktop`). Throws on an unknown id rather than silently recording
 * desktop under the wrong label — a mislabeled artifact is worse than a loud
 * failure, especially once the matrix fans out across profiles.
 */
export function activeProfile(): CameraProfile {
  const id = process.env[PROFILE_ENV];
  if (!id) return PROFILES.desktop;
  if (!(id in PROFILES)) {
    throw new Error(
      `${PROFILE_ENV}="${id}" is not a known camera profile (have: ${Object.keys(PROFILES).join(", ")})`,
    );
  }
  return PROFILES[id as ProfileId];
}

/**
 * The artifact-filename suffix for a profile. Empty for `desktop` so the
 * back-compat primary stays `<base>.mp4` and the entire existing desktop
 * pipeline (record → stage → site) is untouched; `--<id>` for any other profile
 * so its variant sits beside the desktop file without clobbering it.
 */
export function profileSuffix(p: CameraProfile = activeProfile()): string {
  return p.id === "desktop" ? "" : `--${p.id}`;
}

/**
 * The `browser.newContext(...)` options for a recording spec, sourced from the
 * active profile. Pass `recordVideoDir` for a spec that records video (the dir
 * is spec-owned; the size is the profile's, so every section's MP4 shares a
 * canvas); omit it for a screenshot-only spec.
 *
 * Spread the result so a spec keeps the one option object Playwright expects:
 *   const context = await browser.newContext(cameraContext({ recordVideoDir: VIDEO_DIR }));
 */
export function cameraContext(
  opts: { recordVideoDir?: string } = {},
  p: CameraProfile = activeProfile(),
) {
  const size = { width: p.width, height: p.height };
  return {
    viewport: size,
    deviceScaleFactor: p.deviceScaleFactor,
    hasTouch: p.touch,
    isMobile: p.touch,
    ...(opts.recordVideoDir
      ? { recordVideo: { dir: opts.recordVideoDir, size } }
      : {}),
  };
}
