#!/usr/bin/env python3
"""Minimal local WebSocket controller for trying Towline with Codex."""

import asyncio
import json
import sys

from websockets.asyncio.server import serve


ENDPOINT = "ws://127.0.0.1:8765"


def log_frame(heading: str, payload: object) -> None:
    print(f"\n{heading}\n{json.dumps(payload, ensure_ascii=False, indent=2)}", flush=True)


async def main() -> None:
    prompt = " ".join(sys.argv[1:]).strip() or (
        "Explore this repository and explain what it does. Cite the files you used. "
        "Do not modify any files."
    )

    completed = asyncio.get_running_loop().create_future()

    async def handle(ws) -> None:
        if completed.done():
            return
        print(f"Towline connected. Prompt: {prompt}", flush=True)
        await ws.send(json.dumps({"id": "basic-1", "op": "send", "text": prompt}))
        replied = False
        turn_completed = False
        async for message in ws:
            frame = json.loads(message)
            if completed.done():
                return
            if "seq" in frame:
                event = frame.get("event", {})
                method = event.get("method", "event")
                log_frame(f"[{frame['seq']}] {method}", event.get("params", {}))
                if method == "turn/completed":
                    turn_completed = True
            elif frame.get("id") == "basic-1":
                if frame.get("error"):
                    completed.set_exception(RuntimeError(frame["error"]))
                    return
                log_frame("Towline accepted the prompt", frame.get("result"))
                replied = True
            if replied and turn_completed:
                completed.set_result(None)
                return

    async with serve(handle, "127.0.0.1", 8765):
        print(f"Controller listening at {ENDPOINT}; start Towline with this endpoint.", flush=True)
        try:
            await asyncio.wait_for(completed, timeout=300)
        except asyncio.TimeoutError as exc:
            raise RuntimeError("Timed out waiting for Towline and Codex to complete the turn") from exc


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        pass
