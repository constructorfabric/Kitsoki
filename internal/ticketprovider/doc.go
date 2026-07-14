// Package ticketprovider runs provider-neutral ticket interfaces.
//
// The Starlark implementation path treats a provider as a module of pure
// functions named after the ticket interface operations: search(ctx), get(ctx),
// comment(ctx), transition(ctx), list_mine(ctx), and the optional extended ops
// create(ctx), comment_edit(ctx), and comment_reactions(ctx). The Go runner owns
// sidecar loading, HTTP capability policy, auth resolution, and error envelope
// normalization so scripts never need env access or provider-specific Go.
package ticketprovider
