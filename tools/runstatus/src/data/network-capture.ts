/**
 * Browser network capture for bug reports.
 *
 * The server-side fallback HAR only sees JSON-RPC posts after they have already
 * been reduced to "POST /rpc -> HTTP 200". This recorder sits at the browser
 * fetch boundary so a bug report can submit the traffic the operator's page
 * actually observed. The server still parses and scrubs the HAR before filing.
 */

import type { Har, HarEntry, HarHeader } from "./live-source.js";

const CREATOR_NAME = "kitsoki-browser";
const HAR_VERSION = "1.2";
const CAPACITY = 256;
const MAX_TEXT_BYTES = 256 * 1024;

const entries: HarEntry[] = [];
let installed = false;
let originalFetch: typeof fetch | null = null;
let nativeFetch: typeof fetch | null = null;
let installedWindow: FetchWindow | null = null;

interface FetchWindow {
  fetch: typeof fetch;
  location: Location;
  Request: typeof Request;
  Headers: typeof Headers;
}

function pushEntry(entry: HarEntry): void {
  entries.push(entry);
  if (entries.length > CAPACITY) entries.splice(0, entries.length - CAPACITY);
}

function cloneEntry(entry: HarEntry): HarEntry {
  return JSON.parse(JSON.stringify(entry)) as HarEntry;
}

function headersToHar(headers: Headers): HarHeader[] {
  const out: HarHeader[] = [];
  headers.forEach((value, name) => out.push({ name, value }));
  out.sort((a, b) => a.name.localeCompare(b.name));
  return out;
}

function headerValue(headers: HarHeader[], name: string): string {
  const lower = name.toLowerCase();
  return headers.find((h) => h.name.toLowerCase() === lower)?.value ?? "";
}

function queryString(url: string): HarHeader[] {
  try {
    const u = new URL(url);
    return Array.from(u.searchParams.entries()).map(([name, value]) => ({
      name,
      value,
    }));
  } catch {
    return [];
  }
}

function byteLength(text: string): number {
  try {
    return new TextEncoder().encode(text).length;
  } catch {
    return text.length;
  }
}

function truncateText(text: string): string {
  if (byteLength(text) <= MAX_TEXT_BYTES) return text;
  return (
    text.slice(0, MAX_TEXT_BYTES) +
    `\n[truncated by kitsoki browser HAR capture at ${MAX_TEXT_BYTES} bytes]`
  );
}

async function bodyText(body: BodyInit | null | undefined): Promise<string | undefined> {
  if (body == null) return undefined;
  if (typeof body === "string") return truncateText(body);
  if (body instanceof URLSearchParams) return truncateText(body.toString());
  if (body instanceof Blob) return truncateText(await body.text());
  if (body instanceof FormData) {
    const pairs: string[] = [];
    body.forEach((value, key) => {
      pairs.push(`${key}=${value instanceof File ? `[file:${value.name}]` : String(value)}`);
    });
    return truncateText(pairs.join("&"));
  }
  return `[omitted by kitsoki browser HAR capture: ${Object.prototype.toString.call(body)}]`;
}

async function requestBodyText(input: RequestInfo | URL, init?: RequestInit): Promise<string | undefined> {
  if (init?.body !== undefined) return bodyText(init.body);
  if (typeof Request !== "undefined" && input instanceof Request) {
    try {
      return truncateText(await input.clone().text());
    } catch {
      return undefined;
    }
  }
  return undefined;
}

function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  if (init?.method) return init.method.toUpperCase();
  if (typeof Request !== "undefined" && input instanceof Request) return input.method || "GET";
  return "GET";
}

function requestURL(win: FetchWindow, input: RequestInfo | URL): string {
  const raw =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.toString()
        : input.url;
  try {
    return new URL(raw, win.location.href).toString();
  } catch {
    return raw;
  }
}

function requestHeaders(win: FetchWindow, input: RequestInfo | URL, init?: RequestInit): Headers {
  if (init?.headers !== undefined) return new win.Headers(init.headers);
  if (typeof Request !== "undefined" && input instanceof Request) return new win.Headers(input.headers);
  return new win.Headers();
}

function rpcComment(reqText?: string, respText?: string): string {
  if (!reqText) return "";
  try {
    const req = JSON.parse(reqText) as { method?: unknown };
    const method = typeof req.method === "string" ? req.method : "";
    if (!method) return "";
    if (respText) {
      try {
        const resp = JSON.parse(respText) as { error?: { code?: unknown; message?: unknown } };
        if (resp.error) {
          return `json-rpc ${method} error ${String(resp.error.code ?? "")}: ${String(resp.error.message ?? "")}`;
        }
      } catch {
        /* keep method-only comment */
      }
    }
    return `json-rpc ${method}`;
  } catch {
    return "";
  }
}

function setComment(entry: HarEntry, reqText?: string, respText?: string): void {
  const comment = rpcComment(reqText, respText);
  if (comment) entry.comment = comment;
}

/** Install the fetch recorder. Idempotent for the browser process. */
export function installNetworkCapture(win: FetchWindow = window): void {
  if (installed) return;
  installed = true;
  originalFetch = win.fetch;
  nativeFetch = win.fetch.bind(win);
  installedWindow = win;

  win.fetch = (async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const startedMs = Date.now();
    const url = requestURL(win, input);
    const method = requestMethod(input, init);
    const reqHeaders = headersToHar(requestHeaders(win, input, init));
    const reqTextPromise = requestBodyText(input, init).catch(() => undefined);
    const entry: HarEntry = {
      startedDateTime: new Date(startedMs).toISOString(),
      time: 0,
      request: {
        method,
        url,
        headers: reqHeaders,
        queryString: queryString(url),
      },
      response: {
        status: 0,
        headers: [],
      },
    };
    pushEntry(entry);

    reqTextPromise.then((text) => {
      if (!text) return;
      entry.request = {
        ...entry.request,
        postData: {
          mimeType: headerValue(reqHeaders, "content-type"),
          text,
        },
      };
      setComment(entry, text);
    });

    try {
      const resp = await nativeFetch!(input, init);
      entry.time = Date.now() - startedMs;
      const respHeaders = headersToHar(resp.headers);
      entry.response = {
        status: resp.status,
        headers: respHeaders,
      };

      const clone = resp.clone();
      Promise.all([reqTextPromise, clone.text()])
        .then(([reqText, respText]) => {
          const text = truncateText(respText);
          entry.time = Date.now() - startedMs;
          entry.response = {
            ...entry.response,
            content: {
              size: byteLength(respText),
              mimeType: headerValue(respHeaders, "content-type"),
              text,
            },
          };
          setComment(entry, reqText, text);
        })
        .catch(() => undefined);

      return resp;
    } catch (e) {
      entry.time = Date.now() - startedMs;
      entry.response = {
        status: 0,
        headers: [],
        content: {
          mimeType: "text/plain",
          text: e instanceof Error ? e.message : String(e),
        },
      };
      throw e;
    }
  }) as typeof fetch;
}

/** Return a HAR 1.2 snapshot of the browser-observed fetch ring buffer. */
export function snapshotNetworkHar(): Har {
  return {
    log: {
      version: HAR_VERSION,
      creator: { name: CREATOR_NAME, version: "1" },
      entries: entries.map(cloneEntry),
    },
  };
}

/** Test helper: reset the recorder and restore fetch when possible. */
export function __resetNetworkCapture(): void {
  entries.length = 0;
  if (originalFetch && installedWindow) {
    installedWindow.fetch = originalFetch;
  }
  originalFetch = null;
  nativeFetch = null;
  installedWindow = null;
  installed = false;
}
