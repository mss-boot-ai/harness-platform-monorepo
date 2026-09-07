from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path


ADAPTER = Path(__file__).resolve().parents[1] / "deepseek_harness_acp.py"


class DeepSeekHarnessACPAdapterTest(unittest.TestCase):
    def test_bridges_two_acp_prompts_through_one_sdk_instance(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module = root / "deepseek_harness"
            module.mkdir()
            (module / "__init__.py").write_text(
                textwrap.dedent(
                    """
                    from types import SimpleNamespace

                    class DeepSeekHarnessConfig:
                        def __init__(self, **values):
                            self.values = values

                    class DeepSeekHarness:
                        def __init__(self, config):
                            self.config = config
                            self.turn = 0
                        def __enter__(self):
                            return self
                        def __exit__(self, *_args):
                            return None
                        def run(self, prompt, *, session_id=None):
                            self.turn += 1
                            return SimpleNamespace(
                                finish_reason="completed",
                                final_response=f"turn-{self.turn}:{prompt}",
                                session_id=session_id,
                            )
                    """
                ),
                encoding="utf-8",
            )
            workspace = root / "workspace"
            workspace.mkdir()
            environment = {
                "HOME": str(root / "home"),
                "MSS_HARNESS_API_BASE_URL": "https://gateway.example/v1",
                "MSS_HARNESS_API_KEY": "test-only-key",
                "MSS_HARNESS_MODEL": "test-model",
                "PATH": os.environ.get("PATH", ""),
                "PYTHONPATH": str(root),
            }
            process = subprocess.Popen(
                [sys.executable, str(ADAPTER)],
                cwd=workspace,
                env=environment,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            assert process.stdin is not None
            assert process.stdout is not None
            requests = [
                {
                    "jsonrpc": "2.0",
                    "id": "initialize",
                    "method": "initialize",
                    "params": {"protocolVersion": 1, "clientCapabilities": {}},
                },
                {
                    "jsonrpc": "2.0",
                    "id": "new",
                    "method": "session/new",
                    "params": {"cwd": str(workspace), "mcpServers": []},
                },
            ]
            for request in requests:
                process.stdin.write(json.dumps(request) + "\n")
                process.stdin.flush()
                response = json.loads(process.stdout.readline())
                self.assertEqual(response["id"], request["id"])
            session_id = response["result"]["sessionId"]

            for turn, prompt in enumerate(("hello", "again"), start=1):
                process.stdin.write(
                    json.dumps(
                        {
                            "jsonrpc": "2.0",
                            "id": f"prompt-{turn}",
                            "method": "session/prompt",
                            "params": {
                                "sessionId": session_id,
                                "prompt": [{"type": "text", "text": prompt}],
                            },
                        }
                    )
                    + "\n"
                )
                process.stdin.flush()
                update = json.loads(process.stdout.readline())
                completed = json.loads(process.stdout.readline())
                self.assertEqual(update["params"]["sessionId"], session_id)
                self.assertEqual(update["params"]["update"]["content"]["text"], f"turn-{turn}:{prompt}")
                self.assertEqual(completed, {"jsonrpc": "2.0", "id": f"prompt-{turn}", "result": {"stopReason": "end_turn"}})

            process.stdin.close()
            self.assertEqual(process.wait(timeout=5), 0)
            assert process.stderr is not None
            self.assertEqual(process.stderr.read(), "")
            process.stdout.close()
            process.stderr.close()


if __name__ == "__main__":
    unittest.main()
