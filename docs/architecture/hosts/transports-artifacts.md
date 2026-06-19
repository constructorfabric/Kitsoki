# Transport and Artifact Hosts

These hosts move information out of the state machine: to user-facing
transports, durable artifact files, or generated media.

| Handler | Use it for | Reference |
|---|---|---|
| `host.transport.post` | Post a message to a registered transport. | [`../hosts.md#hosttransportpost`](../hosts.md#hosttransportpost) |
| `host.artifacts_dir` | Write markdown or emit media artifacts under the artifact root. | [`../hosts.md#hostartifacts_dir`](../hosts.md#hostartifacts_dir) |
| `host.slidey.render` | Render a JSON scene spec to MP4, PDF, or HTML. | [`../hosts.md#hostslideyrender`](../hosts.md#hostslideyrender) |
| `host.contact_sheet` | Build a PNG contact sheet from image frames. | [`../hosts.md#hostcontact_sheet`](../hosts.md#hostcontact_sheet) |
| `host.video.frame` | Extract one PNG frame from a video. | [`../hosts.md#hostvideoframe`](../hosts.md#hostvideoframe) |

For the transport model itself, see [`../transports.md`](../transports.md). For
trace-side artifact events, see [`../../tracing/trace-format.md`](../../tracing/trace-format.md).
