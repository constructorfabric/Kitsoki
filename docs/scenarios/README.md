# Scenarios

Scenarios are controlled, end-to-end descriptions of workflows we use for
dogfood, demos, and acceptance gates. They sit between proposals and runbooks:
proposals explain what should exist, runbooks explain how to operate it, and
scenarios define the observable flow we intend to prove.

When a scenario changes, update the scenario first, then update scripts,
runbooks, and transient `.context` notes to match it.

| Scenario | Purpose |
|---|---|
| [Live GitHub agent POC](github-agent-live-poc.md) | Prove the live `@kitsoki` GitHub App loop and its primary Slidey deck artifact |

