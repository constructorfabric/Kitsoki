package graph_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/app/graph"
)

func TestRoomGraphAcyclic(t *testing.T) {
	const src = `
app:
  id: acyclic
  version: "1"
root: start
world: {}
intents:
  next: {}
states:
  start:
    description: Start
    on:
      next:
        - target: middle
  middle:
    description: Middle
    on:
      next:
        - target: done
  done:
    description: Done
    terminal: true
`
	def, err := app.LoadBytes([]byte(src))
	require.NoError(t, err)

	g := graph.RoomGraph(app.Compile(def), "test#acyclic")
	require.Equal(t, graph.SchemaV1, g.Schema)
	assert.Equal(t, "room-state-machine", g.Kind)
	assert.True(t, g.Directed)
	assert.False(t, g.Cyclic)
	assert.Len(t, g.Nodes, 3)
	require.Len(t, g.Edges, 2)
	assert.True(t, hasGraphEdge(g, "state:start", "state:middle", "next"))
	assert.True(t, hasGraphEdge(g, "state:middle", "state:done", "next"))
}

func TestRoomGraphPreservesCyclesAndSelfLoops(t *testing.T) {
	const src = `
app:
  id: cyclic
  version: "1"
root: idle
world: {}
intents:
  retry: {}
  next: {}
  back: {}
states:
  idle:
    description: Idle
    on:
      retry:
        - target: idle
      next:
        - target: working
  working:
    description: Working
    on:
      back:
        - target: idle
`
	def, err := app.LoadBytes([]byte(src))
	require.NoError(t, err)

	g := graph.RoomGraph(app.Compile(def), "test#cyclic")
	assert.True(t, g.Cyclic)
	assert.Len(t, g.Nodes, 2)
	assert.Len(t, g.Edges, 3)

	var sawSelfLoop, sawBackEdge bool
	for _, e := range g.Edges {
		if e.Source == "state:idle" && e.Target == "state:idle" && e.Label == "retry" {
			sawSelfLoop = true
		}
		if e.Source == "state:working" && e.Target == "state:idle" && e.Label == "back" {
			sawBackEdge = true
		}
	}
	assert.True(t, sawSelfLoop, "self-loop edge should remain explicit")
	assert.True(t, sawBackEdge, "back edge should remain explicit")
}

func hasGraphEdge(g graph.KitsokiGraph, source, target, label string) bool {
	for _, e := range g.Edges {
		if e.Source == source && e.Target == target && e.Label == label {
			return true
		}
	}
	return false
}
