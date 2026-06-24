# Proposals Process Proposal

Proposals consist of a few key phases:

- Get basic idea or existing docs
    - brief.md
    - gate sanity checks brief.md using agent.decide to continue or clarify, and if clarify, returns clarifying questions (brief-decision.json)
- Check for existing proposals or features that overlap
    - existing-state.md
    - Clarify where the proposal fits in relation to existing roadmap, proposals, features
- Is the idea complete
    - idea-completeness.md
    - What problem is it solving, why is kitsoki the right tool, how the user will use it, etc...
- Gather references
    - references.json
    - Docs, rules, guidelines, etc...
    - Line number sections in docs, rationale, etc...
- Generate proposal
    - proposal.md
- Publish proposal
    - Moves proposal.md to .. real proposals folder, renames it with a meaningful name
    - Completeness check (save all the completeness checks so they can be referred to, use the prefix number to disambiguate)

User can create the skeleton folders in a pre-defined location (maybe docs/proposals/.workspace - keeping files close to where they will end up but in some . folder that can be gitignored across the project).  There is a pre-populated brief.md

The tool should detect if there's a similar idea in progress in .workspace or already accepted into proposals, and propose a change rather than blindly create a new proposal even if the user's original intent was "create a new proposal"

The artifacts need to be numbered sequentially with a 3 digit numerical prefix so lexical sort of strings keeps them in the right order