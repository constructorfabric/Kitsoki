package extdocs

import (
	"path/filepath"
	"testing"
)

const minimalStory = `app:
  id: demo
  version: 0.1.0
  title: Demo Story
world:
  ticket_id: {type: string, default: ""}
intents:
  start: {description: Start}
exports:
  intents: [start]
exits:
  done: {}
host_interfaces:
  reporter:
    default: host.local.reporter
    operations:
      add: {input: {message: string}, output: {ok: bool}}
agents:
  reviewer:
    system_prompt: Review safely.
toolboxes:
  read_only:
    tools: [Read]
providers:
  cheap:
    backend: codex
agent_plugins:
  agent.local:
    plugin: builtin.inprocess
hosts: [host.local.reporter]
root: idle
states:
  idle:
    description: Idle
    on:
      start:
        - target: idle
`

func TestBuildIndex_DiscoversKitStoryAndDocs(t *testing.T) {
	root := t.TempDir()
	kitDir := filepath.Join(root, "kits", "demo-kit")
	storyDir := filepath.Join(kitDir, "stories", "demo")
	writeTestFile(t, filepath.Join(storyDir, "app.yaml"), minimalStory)
	writeTestFile(t, filepath.Join(storyDir, "prompts", "review.md"), "# Review\n")
	writeTestFile(t, filepath.Join(storyDir, "schemas", "out.json"), `{}`)
	writeTestFile(t, filepath.Join(storyDir, "scripts", "enrich.star"), "def main(ctx):\n    return {}\n")
	writeTestFile(t, filepath.Join(storyDir, "flows", "happy.yaml"), "app: ../app.yaml\n")
	writeTestFile(t, filepath.Join(kitDir, "kit.yaml"), `schema: kit/v1
kit: demo
namespace: example
version: 1.0.0
title: Demo Kit
summary: Demo summary
requires:
  kitsoki: ">=0.1.0"
provides:
  stories: [demo]
  schemas: [demo/out]
  interfaces: [reporter]
  ui:
    - id: viewer
      title: Viewer
      entry: viewer
      nav: true
conformance:
  flows: ["stories/demo/flows/*.yaml"]
`)
	writeTestFile(t, filepath.Join(kitDir, ManifestFileName), `schema: kitsoki.docs/v1
owner:
  kind: kit
  id: "@example/demo"
title: Demo Kit Docs
docs:
  - id: overview
    title: Overview
    path: README.md
components:
  - kind: story
    id: demo
    docs:
      - id: contract
        title: Contract
        generated_from: stories/demo/app.yaml
`)

	idx, err := BuildIndex(IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.Packages) != 1 || idx.Packages[0].ID != "@example/demo" {
		t.Fatalf("packages = %#v", idx.Packages)
	}
	if len(idx.Stories) != 1 || idx.Stories[0].ID != "story:@example/demo/demo" {
		t.Fatalf("stories = %#v", idx.Stories)
	}
	story := idx.Stories[0]
	assertContains(t, story.WorldKeys, "ticket_id")
	assertContains(t, story.HostInterfaces, "reporter")
	assertContains(t, story.Agents, "reviewer")
	assertContains(t, story.Prompts, "prompts/review.md")
	assertContains(t, story.Schemas, "schemas/out.json")
	assertContains(t, story.Scripts, "scripts/enrich.star")
	assertContains(t, story.Flows, "flows/happy.yaml")
	if len(idx.Docs) != 2 {
		t.Fatalf("docs len = %d, want 2: %#v", len(idx.Docs), idx.Docs)
	}
	assertComponent(t, idx.Components, "story", "story:@example/demo/demo")
	assertComponent(t, idx.Components, "host-interface", "story:@example/demo/demo#host_interfaces.reporter")
	assertComponent(t, idx.Components, "agent-profile", "story:@example/demo/demo#agents.reviewer")
	assertComponent(t, idx.Components, "provider-profile", "story:@example/demo/demo#providers.cheap")
	assertComponent(t, idx.Components, "toolbox", "story:@example/demo/demo#toolboxes.read_only")
	assertComponent(t, idx.Components, "agent-plugin", "story:@example/demo/demo#agent_plugins.agent.local")
	assertComponent(t, idx.Components, "prompt", "story:@example/demo/demo#prompts/review.md")
	assertComponent(t, idx.Components, "schema", "story:@example/demo/demo#schemas/out.json")
	assertComponent(t, idx.Components, "starlark-script", "story:@example/demo/demo#scripts/enrich.star")
}

func TestBuildIndex_DiscoversStandaloneStory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "stories", "solo", "app.yaml"), minimalStory)

	idx, err := BuildIndex(IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.Packages) != 0 {
		t.Fatalf("packages = %#v, want none", idx.Packages)
	}
	if len(idx.Stories) != 1 || idx.Stories[0].ID != "story:@local/solo" {
		t.Fatalf("stories = %#v", idx.Stories)
	}
}

func assertContains(t *testing.T, got []string, want string) {
	t.Helper()
	for _, v := range got {
		if v == want {
			return
		}
	}
	t.Fatalf("%q not found in %#v", want, got)
}

func assertComponent(t *testing.T, got []Component, kind, id string) {
	t.Helper()
	for _, c := range got {
		if c.Kind == kind && c.ID == id {
			return
		}
	}
	t.Fatalf("component %s:%s not found in %#v", kind, id, got)
}
