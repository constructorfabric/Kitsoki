// Package corpusintake normalizes candidate sources into the Corpus Forge
// corpus-case.v1 contract. It sits before corpus proof: adapters describe a
// reproducible proposed task, while the proof executor independently decides
// whether that task may be admitted.
//
// # Contract
//
// Every adapter returns a [Candidate]. Candidate is deliberately JSON-shaped so
// a Story Starlark script, Studio MCP client, and proof executor can share the
// same stable document. Its Kind is always [KindCorpusCaseV1]. `verified_red`
// and `verified_green` are intentionally absent: imported assertions are not
// proof.
//
// # Discovery
//
// Manifest imports are offline and reviewable. Local history discovery is
// dependency-injected through [HistorySource] and disabled unless callers set
// [DiscoveryOptions.AllowLive]; this package neither shells out to git nor
// reaches the network.
//
// # Non-goals
//
//   - This package does not run an oracle or admit a candidate. That belongs to
//     the corpus proof seam, preventing source self-report from becoming proof.
//   - This package does not scrape remote issue trackers. Such discovery must
//     be explicit and supply a reviewable HistorySource result.
package corpusintake
