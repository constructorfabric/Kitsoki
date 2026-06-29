# tui-dwf-test app

This is the app copy exported for the `tui-dwf-test` dynamic workflow. The
exported package keeps the punch-list story template under `app/` and rewrites
the surrounding manifest, launch file, traces root, and generated flow under
`stories/tui-dwf-test/`.

Run the exported story's no-LLM flow checks with:

```bash
go run ./cmd/kitsoki test flows stories/tui-dwf-test/app/app.yaml
```

The generated manifest is `stories/tui-dwf-test/manifest.yaml`; the generated
flow is `stories/tui-dwf-test/flows/generated.yaml`.
