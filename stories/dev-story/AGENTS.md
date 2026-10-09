This is the flagship general-purpose project workflow story. It must work when
Kitsoki is tooling around an arbitrary target repository, including a freshly
initialized `.kitsoki/` wrapper for projects such as Kubernetes, Postgres,
Slidey, or a private application.

The quality is critical here - we intend for it to be re-used and a major
entry/selling point for Kitsoki. It should have golden examples, be
exceptionally well tested and documented. Do not make the target project
implicitly be Kitsoki; refer to Kitsoki as the workflow/tooling layer and keep
project-specific examples neutral unless a fixture explicitly says otherwise.

For every dev-story intake, router, workbench, imported-story handoff, and
agent/host dispatch, apply `../../docs/stories/story-style.md` §3.4. The operator
should see a terse confirmation of interpreted parameters, selected story/path,
mode/consequence, and next action before work starts. Do not add confirmation
turns unless input/action is actually required; add flow assertions for the
message and resulting view.

@README.md
