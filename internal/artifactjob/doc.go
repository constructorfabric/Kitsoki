// Package artifactjob persists the durable product identity for long-running,
// artifact-producing work.
//
// It deliberately does not schedule goroutines. internal/jobs continues to own
// background execution; artifactjob records the user-facing job row that can be
// listed, reattached, linked to a run URL, and indexed for artifacts after the
// process that created it has exited.
package artifactjob
