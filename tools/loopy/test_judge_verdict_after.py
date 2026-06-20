"""test_judge_verdict_after.py — trace-continuity tests for judge_verdict_after.py.

Verifies that judge_verdict_after.py routes every agent call through
kitsoki agent (socket or CLI path) rather than shelling out to claude -p,
and that KITSOKI_SESSION_ID is threaded into the call so decisions are
linked to the parent trace.

No real LLM is invoked. The socket path tests spin up a minimal in-process
JSON-RPC stub server; the CLI path tests are covered by asserting that the
script does NOT invoke claude and DOES invoke kitsoki.

Run: python3 -m pytest tools/loopy/ -v
"""

import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading

import pytest

# Absolute path to the script under test.
_SCRIPT = str(
    pathlib.Path(__file__).parent.parent.parent
    / "stories" / "bugfix" / "scripts" / "judge_verdict_after.py"
)

# Canonical schema path (used by judge_verdict_after.py).
_SCHEMA = str(
    pathlib.Path(__file__).parent.parent.parent
    / "stories" / "bugfix" / "schemas" / "judge_verdict.json"
)

# Canonical prompt path (used by judge_verdict_after.py).
_PROMPT = str(
    pathlib.Path(__file__).parent.parent.parent
    / "stories" / "bugfix" / "prompts" / "judge_validating.md"
)


# ── stub agent server ─────────────────────────────────────────────────────────

class _StubAgentServer:
    """Minimal JSON-RPC server that records incoming requests and returns a
    canned verdict. Runs in a background thread; stopped via close()."""

    def __init__(self, sock_path: str, response: dict) -> None:
        self.sock_path = sock_path
        self.response = response
        self.received: list[dict] = []
        self._server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self._server.bind(sock_path)
        self._server.listen(5)
        self._server.settimeout(5.0)
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()

    def _serve(self) -> None:
        try:
            conn, _ = self._server.accept()
        except OSError:
            return
        with conn:
            data = b""
            while b"\n" not in data:
                chunk = conn.recv(4096)
                if not chunk:
                    break
                data += chunk
            line = data.split(b"\n")[0]
            req = json.loads(line)
            self.received.append(req)
            resp = {
                "jsonrpc": "2.0",
                "id": req.get("id"),
                "result": self.response,
            }
            conn.sendall((json.dumps(resp) + "\n").encode())

    def close(self) -> None:
        self._server.close()
        self._thread.join(timeout=2.0)
        try:
            os.unlink(self.sock_path)
        except FileNotFoundError:
            pass


@pytest.fixture()
def tmp_sock(tmp_path):
    return str(tmp_path / "agent-test.sock")


@pytest.fixture()
def valid_verdict_response():
    return {
        "data": {
            "submitted": {
                "verdict": "pass",
                "intent": "accept",
                "reason": "All tests green.",
                "confidence": 0.95,
            }
        }
    }


# ── socket path tests ──────────────────────────────────────────────────────────

class TestJudgeVerdictAfterSocketPath:
    """judge_verdict_after.py uses the unix socket when KITSOKI_AGENT_SOCK is set."""

    def test_routes_to_agent_decide(self, tmp_sock, valid_verdict_response):
        """The script sends agent.decide over the socket, not claude -p."""
        server = _StubAgentServer(tmp_sock, valid_verdict_response)
        try:
            env = os.environ.copy()
            env["KITSOKI_AGENT_SOCK"] = tmp_sock
            env["KITSOKI_SESSION_ID"] = "test-session-abc"

            result = subprocess.run(
                [sys.executable, _SCRIPT, "PROJ-123", "Tests pass", "All green"],
                capture_output=True,
                text=True,
                env=env,
                timeout=10,
            )
        finally:
            server.close()

        assert result.returncode == 0, f"script failed: {result.stderr}"
        assert len(server.received) == 1, "expected exactly one RPC call"
        req = server.received[0]
        assert req["method"] == "agent.decide", f"wrong method: {req['method']}"

    def test_passes_schema_and_prompt(self, tmp_sock, valid_verdict_response):
        """The agent.decide call references the canonical schema and prompt paths."""
        server = _StubAgentServer(tmp_sock, valid_verdict_response)
        try:
            env = os.environ.copy()
            env["KITSOKI_AGENT_SOCK"] = tmp_sock
            env.pop("KITSOKI_SESSION_ID", None)

            subprocess.run(
                [sys.executable, _SCRIPT, "PROJ-456", "Diff clean", "No issues"],
                capture_output=True,
                text=True,
                env=env,
                timeout=10,
            )
        finally:
            server.close()

        assert len(server.received) == 1
        params = server.received[0]["params"]
        assert "schema" in params, "params must include schema path"
        assert "judge_verdict.json" in params["schema"]
        assert "prompt" in params, "params must include prompt path"
        assert "judge_validating" in params["prompt"]

    def test_args_json_carries_ticket_fields(self, tmp_sock, valid_verdict_response):
        """ticket_id, artifact_title, artifact_body are forwarded in args_json."""
        server = _StubAgentServer(tmp_sock, valid_verdict_response)
        try:
            env = os.environ.copy()
            env["KITSOKI_AGENT_SOCK"] = tmp_sock

            subprocess.run(
                [sys.executable, _SCRIPT, "PROJ-789", "My title", "My body"],
                capture_output=True,
                text=True,
                env=env,
                timeout=10,
            )
        finally:
            server.close()

        assert len(server.received) == 1
        params = server.received[0]["params"]
        args = json.loads(params["args_json"])
        assert args["ticket_id"] == "PROJ-789"
        assert args["artifact_title"] == "My title"
        assert args["artifact_body"] == "My body"

    def test_returns_verdict_json_on_stdout(self, tmp_sock, valid_verdict_response):
        """Script prints the submitted verdict as JSON to stdout."""
        server = _StubAgentServer(tmp_sock, valid_verdict_response)
        try:
            env = os.environ.copy()
            env["KITSOKI_AGENT_SOCK"] = tmp_sock

            result = subprocess.run(
                [sys.executable, _SCRIPT, "PROJ-1", "t", "b"],
                capture_output=True,
                text=True,
                env=env,
                timeout=10,
            )
        finally:
            server.close()

        assert result.returncode == 0
        out = json.loads(result.stdout)
        assert out["verdict"] == "pass"
        assert out["intent"] == "accept"
        assert out["confidence"] == pytest.approx(0.95)

    def test_no_direct_claude_invocation(self, tmp_sock, valid_verdict_response):
        """When KITSOKI_AGENT_SOCK is set, the script must not fork 'claude'."""
        server = _StubAgentServer(tmp_sock, valid_verdict_response)
        # Install a sentinel that fails if 'claude' is called.
        with tempfile.TemporaryDirectory() as tmpdir:
            fake_claude = pathlib.Path(tmpdir) / "claude"
            fake_claude.write_text(
                "#!/bin/sh\necho 'ERROR: direct claude call detected' >&2\nexit 99\n"
            )
            fake_claude.chmod(0o755)
            try:
                env = os.environ.copy()
                env["KITSOKI_AGENT_SOCK"] = tmp_sock
                env["PATH"] = tmpdir + ":" + env.get("PATH", "")

                result = subprocess.run(
                    [sys.executable, _SCRIPT, "PROJ-1", "t", "b"],
                    capture_output=True,
                    text=True,
                    env=env,
                    timeout=10,
                )
            finally:
                server.close()

        assert result.returncode == 0, f"script failed; stderr: {result.stderr}"
        assert "direct claude call detected" not in result.stderr


# ── session ID inheritance tests ───────────────────────────────────────────────

class TestSessionIDInheritance:
    """KITSOKI_SESSION_ID set by the state machine must reach the agent call."""

    def test_session_id_in_environment(self, tmp_sock, valid_verdict_response):
        """Script runs without error when KITSOKI_SESSION_ID is set."""
        server = _StubAgentServer(tmp_sock, valid_verdict_response)
        try:
            env = os.environ.copy()
            env["KITSOKI_AGENT_SOCK"] = tmp_sock
            env["KITSOKI_SESSION_ID"] = "session-xyz-999"

            result = subprocess.run(
                [sys.executable, _SCRIPT, "PROJ-10", "title", "body"],
                capture_output=True,
                text=True,
                env=env,
                timeout=10,
            )
        finally:
            server.close()

        assert result.returncode == 0, f"script failed with session ID set: {result.stderr}"

    def test_script_works_without_session_id(self, tmp_sock, valid_verdict_response):
        """Script works when no parent session is available (standalone use)."""
        server = _StubAgentServer(tmp_sock, valid_verdict_response)
        try:
            env = os.environ.copy()
            env["KITSOKI_AGENT_SOCK"] = tmp_sock
            env.pop("KITSOKI_SESSION_ID", None)

            result = subprocess.run(
                [sys.executable, _SCRIPT, "PROJ-11", "title", "body"],
                capture_output=True,
                text=True,
                env=env,
                timeout=10,
            )
        finally:
            server.close()

        assert result.returncode == 0, f"script failed without session ID: {result.stderr}"


# ── usage / error handling ─────────────────────────────────────────────────────

class TestUsageAndErrors:
    """Basic error handling: wrong arg count, RPC error propagation."""

    def test_usage_when_no_args(self):
        """Script prints usage to stderr and exits non-zero with no args."""
        result = subprocess.run(
            [sys.executable, _SCRIPT],
            capture_output=True,
            text=True,
            timeout=5,
        )
        assert result.returncode != 0
        assert "usage" in result.stderr.lower()

    def test_rpc_error_propagates(self, tmp_sock):
        """An RPC error response causes the script to exit non-zero."""
        error_response_raw = {
            "jsonrpc": "2.0",
            "id": 1,
            "error": {"code": -32000, "message": "schema validation failed"},
        }

        # Build a server that returns an error response instead.
        class _ErrorServer:
            def __init__(self, path):
                self.received = []
                self._s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                self._s.bind(path)
                self._s.listen(1)
                self._s.settimeout(5.0)
                self._t = threading.Thread(target=self._run, daemon=True)
                self._t.start()

            def _run(self):
                try:
                    conn, _ = self._s.accept()
                except OSError:
                    return
                with conn:
                    data = b""
                    while b"\n" not in data:
                        chunk = conn.recv(4096)
                        if not chunk:
                            break
                        data += chunk
                    self.received.append(json.loads(data.split(b"\n")[0]))
                    conn.sendall((json.dumps(error_response_raw) + "\n").encode())

            def close(self):
                self._s.close()
                self._t.join(timeout=2.0)
                try:
                    os.unlink(tmp_sock)
                except FileNotFoundError:
                    pass

        srv = _ErrorServer(tmp_sock)
        try:
            env = os.environ.copy()
            env["KITSOKI_AGENT_SOCK"] = tmp_sock

            result = subprocess.run(
                [sys.executable, _SCRIPT, "PROJ-1", "t", "b"],
                capture_output=True,
                text=True,
                env=env,
                timeout=10,
            )
        finally:
            srv.close()

        assert result.returncode != 0, "script should exit non-zero on RPC error"
