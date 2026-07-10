# Credential conventions

How kitsoki tools find credentials so operators (and the scripts wrapping
them) never paste secrets around by hand. This is the local, single-tenant
first step; the layout is deliberately tenant-shaped so a future multi-tenant
credential gateway (a broker serving short-lived tokens over an authenticated
channel) can replace the file lookups **behind the same env names** without
consumers changing.

## The store

One root, fixed layout, private permissions (0700 dirs, 0600 files):

```
$KITSOKI_CREDENTIALS_DIR                 default: ~/.config/kitsoki
├── gh-app/
    ├── <app-slug>/                      one profile per GitHub App
    │   ├── kitsoki.env                  ids + secrets (KITSOKI_GH_APP_*)
    │   └── gh-app.pem                   the App's RSA private key
    ├── default -> <app-slug>            the active profile (symlink)
    └── tokens/<client-id>.json          cached user-to-server tokens
└── github.env                           short-lived GH_TOKEN/GITHUB_TOKEN for local shells
```

`kitsoki gh-agent setup app` populates a profile and points `default` at it.
Hand-made Apps get a profile by writing the same two files (copy the env
names below).

`kitsoki gh-agent token` mints a GitHub App installation token from the active
profile and writes `github.env` (0600). Source that file before local commands
that open issues, open PRs, push branches/artifacts, or comment on GitHub:

```
source ~/.config/kitsoki/github.env
```

The env file is intentionally refreshable because GitHub App installation
tokens are short-lived. Manual PAT fallback is explicit:
`GH_TOKEN=<fine-grained-pat> kitsoki gh-agent token --from-env`.

## Env names

Fixed; scripts and CI use exactly these:

| Name | What |
|---|---|
| `KITSOKI_CREDENTIALS_DIR` | store root override |
| `KITSOKI_GH_APP_ID` | GitHub App id |
| `KITSOKI_GH_APP_INSTALLATION_ID` | installation id |
| `KITSOKI_GH_APP_PRIVATE_KEY_FILE` | path to the `.pem` (key by path, never inline) |
| `KITSOKI_GH_WEBHOOK_SECRET` | webhook HMAC secret |
| `KITSOKI_GH_APP_CLIENT_ID` | App OAuth client id |
| `KITSOKI_GH_APP_CLIENT_SECRET` | App OAuth client secret |
| `KITSOKI_GH_APP_ENV_FILE` | explicit profile env file (bypasses the default profile) |
| `GH_TOKEN` / `GITHUB_TOKEN` | GitHub API token consumed by local GitHub host surfaces; usually written by `kitsoki gh-agent token` |

## Precedence

Everywhere, most-explicit wins:

1. command flags
2. process environment
3. `--env-file` / `$KITSOKI_GH_APP_ENV_FILE`
4. the store's `default` profile

Commands print **which source** supplied credentials (never the values).

## Rules

- **Secrets never enter a repo.** Env files and keys live in the store or in
  service config (`/etc/kitsoki/…`), not in checkouts. The conventions-pack
  `pog-doctor` warns on tracked `*.pem`/`*.env` files.
- **Keys by file path.** Private keys are referenced by path
  (`…_PRIVATE_KEY_FILE`), never inlined into env values or argv.
- **Prefer env/file over flags for secrets** — argv is visible to other local
  processes; the `--client-secret` flag exists for scripting but the env file
  is the convention.
- **Harness profiles name env vars, not key values.** The dev-story local
  harness setup asks for names such as `OPENAI_API_KEY` or `SYNTHETIC_API_KEY`
  and writes `${VAR}` references into `.kitsoki.local.yaml`; it refuses raw
  key-looking input.
- **CI**: platform secrets map to the same env names (e.g. GitHub Actions
  `secrets.*` → `env:`); no checked-in credential files, no new names.
- **Services**: the daemon env file (`/etc/kitsoki/gh-agent.env`) uses the
  same names — a profile env file can be copied there verbatim.

## Trajectory

Multi-tenant/cloud: profiles become tenants; the store lookup becomes a
gateway call that returns short-lived scoped tokens (device-flow-style
consent, audit trail, revocation). Consumers keep reading the same env names;
only the resolver changes. Tracked in the POG program catalog as
`ask-credential-gateway`.
