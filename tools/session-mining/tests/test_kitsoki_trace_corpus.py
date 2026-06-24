import importlib.util
import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("kitsoki_trace_corpus", ROOT / "kitsoki_trace_corpus.py")
corpus = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(corpus)


def write_jsonl(path, rows):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as f:
        for row in rows:
            f.write(json.dumps(row) + "\n")


class KitsokiTraceCorpusTest(unittest.TestCase):
    def test_build_samples_pairs_turn_input_with_transition(self):
        with self.subTest("non-empty filter and expected transition"):
            from tempfile import TemporaryDirectory

            with TemporaryDirectory() as td:
                tmp_path = Path(td)
                trace = tmp_path / "kitsoki-dev" / "abc-web-session.jsonl"
                write_jsonl(
                    trace,
                    [
                        {
                            "kind": "turn.input",
                            "turn": 1,
                            "state_path": "core.main",
                            "payload": {"input": "tickets", "intent": "core__go_ticket_search"},
                        },
                        {
                            "kind": "machine.transition",
                            "turn": 1,
                            "state_path": "core.main",
                            "payload": {
                                "from": "core.main",
                                "intent": "core__go_ticket_search",
                                "slots": {},
                                "to": "core.ticket_search",
                            },
                        },
                        {
                            "kind": "turn.input",
                            "turn": 2,
                            "state_path": "core.ticket_search",
                            "payload": {"input": "", "intent": "core__pick_ticket"},
                        },
                        {
                            "kind": "machine.transition",
                            "turn": 2,
                            "state_path": "core.ticket_search",
                            "payload": {
                                "from": "core.ticket_search",
                                "intent": "core__pick_ticket",
                                "slots": {"n": 3},
                                "to": "core.ticket_search",
                            },
                        },
                        {
                            "kind": "turn.input",
                            "turn": 3,
                            "state_path": "core.landing",
                            "payload": {"input": "ok go ahead", "intent": "core__drive"},
                        },
                        {
                            "kind": "machine.transition",
                            "turn": 3,
                            "state_path": "core.landing",
                            "payload": {
                                "from": "core.landing",
                                "intent": "core__drive",
                                "slots": {},
                                "to": "core.bf.idle",
                            },
                        },
                    ],
                )

                files = corpus.iter_trace_files(tmp_path, "kitsoki-dev")
                non_empty = corpus.build_samples(tmp_path, files, include_empty=False)
                all_samples = corpus.build_samples(tmp_path, files, include_empty=True)

                self.assertEqual(len(non_empty), 2)
                self.assertEqual(non_empty[0]["input"], "tickets")
                self.assertEqual(non_empty[0]["expected"]["intent"], "core__go_ticket_search")
                self.assertIs(non_empty[0]["route_labeled"], True)
                self.assertIs(non_empty[1]["context_dependent"], True)

                self.assertEqual(len(all_samples), 3)
                self.assertEqual(all_samples[1]["input"], "")
                self.assertEqual(all_samples[1]["expected"]["slots"], {"n": 3})

                fixture_count = corpus.write_intent_fixtures(tmp_path / "out", all_samples)
                self.assertEqual(fixture_count, 2)
                main_fixture = tmp_path / "out" / "intent-fixtures" / "kitsoki-dev" / "core.main.yaml"
                self.assertIn("test_kind: intents", main_fixture.read_text(encoding="utf-8"))
                self.assertIn('name: "core__go_ticket_search"', main_fixture.read_text(encoding="utf-8"))
                landing_fixture = tmp_path / "out" / "intent-fixtures" / "kitsoki-dev" / "core.landing.yaml"
                self.assertFalse(landing_fixture.exists())

                fixture_count = corpus.write_intent_fixtures(
                    tmp_path / "out-with-context",
                    all_samples,
                    include_contextual=True,
                )
                self.assertEqual(fixture_count, 3)
                landing_fixture = tmp_path / "out-with-context" / "intent-fixtures" / "kitsoki-dev" / "core.landing.yaml"
                self.assertIn('name: "core__drive"', landing_fixture.read_text(encoding="utf-8"))

    def test_build_transcript_prompts_are_not_route_labeled(self):
        from tempfile import TemporaryDirectory

        with TemporaryDirectory() as td:
            tmp_path = Path(td)
            transcript = tmp_path / "kitsoki-dev" / "transcripts" / "agent-session.jsonl"
            write_jsonl(
                transcript,
                [
                    {
                        "type": "user",
                        "session_id": "s1",
                        "message": {
                            "content": [
                                {"type": "text", "text": "Search the repo for tickets."},
                                {"type": "tool_result", "content": "ignored"},
                            ]
                        },
                    }
                ],
            )

            prompts = corpus.build_transcript_prompts(tmp_path, [transcript])

            self.assertEqual(len(prompts), 1)
            self.assertEqual(prompts[0]["input"], "Search the repo for tickets.")
            self.assertIs(prompts[0]["route_labeled"], False)
            self.assertEqual(prompts[0]["source"], "kitsoki_embedded_agent_transcript")


if __name__ == "__main__":
    unittest.main()
