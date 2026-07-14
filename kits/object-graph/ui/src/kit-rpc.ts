// kit-rpc.ts — a minimal, self-contained JSON-RPC client for this kit's UI
// module. Deliberately NOT a copy of the host SPA's tools/runstatus/src/
// transport/jsonrpc.ts JsonRpcClient (SSE subscriptions, VS Code webview
// bridge detection, retry/backoff, ...) — this kit only ever needs one
// synchronous request/response call: kit.object-graph.graph.<op> over the
// same POST /rpc endpoint the host SPA uses. D3's full "shared ui-sdk"
// vision (a semver'd @kitsoki/ui-sdk providing this transport as a common
// component) is deferred — see kits/object-graph/ui/README (S5 PR
// description) for why: it needs the SPA build to stop inlining everything
// (vite-plugin-singlefile) and externalize vue via an import map, which is a
// separately-scoped SPA build change S3c already deferred once. Until that
// lands, every kit UI module ships its own tiny transport like this one.
let nextId = 1;

export interface KitRpcError {
  code: number;
  message: string;
}

export async function kitRpcCall<T = unknown>(
  method: string,
  params: Record<string, unknown>,
): Promise<T> {
  const res = await fetch("/rpc", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: nextId++, method, params }),
  });
  if (!res.ok) {
    throw new Error(`kit-rpc: ${method}: HTTP ${res.status}`);
  }
  const body = (await res.json()) as {
    result?: T;
    error?: KitRpcError;
  };
  if (body.error) {
    throw new Error(`kit-rpc: ${method}: ${body.error.message}`);
  }
  return body.result as T;
}

/** Calls kit.object-graph.graph.<op> — this kit's own declared interface. */
export function graphOp<T = unknown>(op: string, params: Record<string, unknown>): Promise<T> {
  return kitRpcCall<T>(`kit.object-graph.graph.${op}`, params);
}
