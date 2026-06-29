# process-design-workflow app

This is the app copy exported for the `process-design-workflow` dynamic
workflow. The exported package keeps the punch-list story template under `app/`
and rewrites the surrounding manifest, launch file, traces root, and generated
flow under `stories/process-design-workflow/`.

Run the exported story's no-LLM flow checks with:

```bash
go run ./cmd/kitsoki test flows stories/process-design-workflow/app/app.yaml
```

The generated manifest is `stories/process-design-workflow/manifest.yaml`; the
generated flow is `stories/process-design-workflow/flows/generated.yaml`.
