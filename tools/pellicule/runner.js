/**
 * PELLICULE — Live HTTP Request Runner
 *
 * Executes real HTTP requests for non-mock scenes.
 * Handles {{variable}} template substitution and JSONPath capture.
 *
 * Dependencies: jsonpath-plus (npm install jsonpath-plus)
 * Uses Node 18+ built-in fetch.
 *
 * JSON format notes (designed to be portable to Go or other languages):
 *   - Template syntax:  {{varName}}   (Postman-compatible)
 *   - Capture syntax:   $.path.to.val (standard JSONPath, RFC 9535)
 *   - Go JSONPath lib:  github.com/PaesslerAG/jsonpath
 */

'use strict';

const { JSONPath } = require('jsonpath-plus');

// ── Status text table ──────────────────────────────────────────────────────

const STATUS_TEXTS = {
  200: 'OK',                    201: 'Created',
  202: 'Accepted',              204: 'No Content',
  301: 'Moved Permanently',     302: 'Found',
  304: 'Not Modified',
  400: 'Bad Request',           401: 'Unauthorized',
  403: 'Forbidden',             404: 'Not Found',
  405: 'Method Not Allowed',    409: 'Conflict',
  410: 'Gone',                  422: 'Unprocessable Entity',
  429: 'Too Many Requests',
  500: 'Internal Server Error', 502: 'Bad Gateway',
  503: 'Service Unavailable',   504: 'Gateway Timeout',
};

// ── Template interpolation ─────────────────────────────────────────────────

/**
 * Substitute {{varName}} placeholders using the provided context.
 * Unknown variables are left as-is ({{varName}}).
 */
function interpolate(str, context) {
  if (typeof str !== 'string') return str;
  return str.replace(/\{\{([^}]+)\}\}/g, (_, key) => {
    const val = context[key.trim()];
    return val !== undefined ? String(val) : `{{${key}}}`;
  });
}

/**
 * Recursively interpolate all string values in an object/array/string.
 */
function interpolateDeep(value, context) {
  if (typeof value === 'string')  return interpolate(value, context);
  if (Array.isArray(value))       return value.map(v => interpolateDeep(v, context));
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value).map(([k, v]) => [k, interpolateDeep(v, context)])
    );
  }
  return value;
}

// ── JSONPath capture ───────────────────────────────────────────────────────

/**
 * Extract values from a parsed JSON body using JSONPath expressions,
 * and merge them into context.
 *
 * @param {string|object} body    - Raw response body (string or parsed)
 * @param {object} captureRules   - { varName: "$.json.path" }
 * @param {object} context        - Mutable context object to update
 */
function applyCapture(body, captureRules, context) {
  if (!captureRules || Object.keys(captureRules).length === 0) return;

  let parsed;
  try {
    parsed = typeof body === 'string' ? JSON.parse(body) : body;
  } catch {
    return; // Non-JSON body — capture silently skipped
  }

  for (const [varName, path] of Object.entries(captureRules)) {
    try {
      const results = JSONPath({ path, json: parsed, wrap: true });
      if (results.length > 0) {
        context[varName] = results[0];
      } else {
        console.warn(`[pellicule/runner] capture: no match for "${path}" → ${varName}`);
      }
    } catch (err) {
      console.warn(`[pellicule/runner] capture: invalid path "${path}": ${err.message}`);
    }
  }
}

// ── Request execution ─────────────────────────────────────────────────────

/**
 * Execute a live HTTP request for a scene, updating context from capture rules.
 *
 * @param {object} scene    - Scene definition (type: 'request', mock: false/omitted)
 * @param {object} context  - Mutable variable context (mutated in-place by capture)
 * @returns {Promise<object>} Populated response object matching the demo schema
 */
async function executeRequest(scene, context) {
  // Interpolate all {{vars}} in the request definition
  const req = interpolateDeep(scene.request, context);

  const method  = (req.method || 'GET').toUpperCase();
  const url     = req.url;
  const headers = {};

  for (const h of req.headers || []) {
    headers[h.name] = h.value;
  }

  // Body handling: raw string passed through
  let body;
  if (req.body && req.body.trim()) {
    body = req.body;
    // Set Content-Type if not already specified
    if (!Object.keys(headers).some(k => k.toLowerCase() === 'content-type')) {
      headers['Content-Type'] = 'application/json';
    }
  }

  // Execute
  let fetchRes;
  let bodyText = '';
  try {
    fetchRes = await fetch(url, {
      method,
      headers,
      ...(body ? { body } : {}),
    });
    bodyText = await fetchRes.text();
  } catch (err) {
    // Network / DNS errors — return synthetic error response
    return {
      status:     0,
      statusText: 'Network Error',
      headers:    [],
      body:       err.message,
      error:      true,
    };
  }

  // Capture values from response into context
  applyCapture(bodyText, scene.capture || {}, context);

  // Assertion check (throws with message if violated)
  if (scene.expect && scene.expect.status !== undefined) {
    if (fetchRes.status !== scene.expect.status) {
      throw new AssertionError(
        `Status assertion failed: expected ${scene.expect.status}, got ${fetchRes.status}`,
        fetchRes.status,
        scene.expect.status,
      );
    }
  }

  // Format response headers as array
  const resHeaders = [];
  fetchRes.headers.forEach((value, name) => {
    // Skip internal / noisy headers
    if (!['transfer-encoding', 'connection'].includes(name.toLowerCase())) {
      resHeaders.push({ name, value });
    }
  });

  // Pretty-print JSON body if possible
  let formattedBody = bodyText;
  const ct = fetchRes.headers.get('content-type') || '';
  if (ct.includes('json') || (bodyText.trim().startsWith('{') || bodyText.trim().startsWith('['))) {
    try {
      formattedBody = JSON.stringify(JSON.parse(bodyText), null, 2);
    } catch { /* keep raw */ }
  }

  return {
    status:     fetchRes.status,
    statusText: STATUS_TEXTS[fetchRes.status] || fetchRes.statusText || '',
    headers:    resHeaders,
    body:       formattedBody,
  };
}

// ── AssertionError ────────────────────────────────────────────────────────

class AssertionError extends Error {
  constructor(message, actual, expected) {
    super(message);
    this.name    = 'AssertionError';
    this.actual   = actual;
    this.expected = expected;
  }
}

module.exports = { executeRequest, interpolate, interpolateDeep, AssertionError };
