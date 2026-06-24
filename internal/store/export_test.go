// Package store — export_test.go: test-only seam exports.
//
// This file is in "package store" (internal) so it is compiled only during
// testing. It exposes the private hookFsync / hookWrite fields of JSONLSink
// to the external store_test package via exported setter methods. Because Go
// merges all test files in a directory into one binary, the setters here and
// the struct fields in jsonl.go share the same memory at test time.
//
// Per-sink hooks are used (rather than package-level globals) so parallel
// tests each set hooks on their own sink without interfering with other tests.
package store

import "os"

// SetHookFsync installs fn as the fsync injection hook for this JSONLSink.
// Pass nil to restore default (real fsync) behaviour.
// Not goroutine-safe: call only in test setup, never during concurrent Appends.
func (s *JSONLSink) SetHookFsync(fn func(*os.File) error) { s.hookFsync = fn }

// SetHookWrite installs fn as the write injection hook for this JSONLSink.
// Pass nil to restore default behaviour.
func (s *JSONLSink) SetHookWrite(fn func(*os.File, []byte) (int, error)) { s.hookWrite = fn }
