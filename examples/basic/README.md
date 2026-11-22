# Basic local controller

This example starts a tiny local WebSocket controller, sends one prompt to Codex through Towline, then prints the reply and streamed Codex events. Run the controller and Towline in separate terminals. Towline connects outward to the controller at `ws://127.0.0.1:8765`, so no hosted controller is needed.

## Requirements

- Go 1.23 or later
- Python 3.10 or later
- Codex CLI available as `codex` on `PATH`, with `app-server` support
- Codex already authenticated

Create a virtual environment and install the Python WebSocket library:

```sh
python3 -m venv .venv
source .venv/bin/activate
python3 -m pip install "websockets>=13"
```

In terminal 1, start the controller from the repository root:

```sh
python3 examples/basic/controller.py
```

In terminal 2, start Towline from the repository root with the matching endpoint:

```sh
go run ./cmd/towline -endpoint ws://127.0.0.1:8765 -dir .
```

By default, Codex gets this prompt: “Explore this repository and explain what it does. Cite the files you used. Do not modify any files.” You can also pass your own prompt as an argument:

```sh
python3 examples/basic/controller.py "Find a small usability issue and suggest a fix. Do not edit files."
```

Towline runs with the repository as Codex's working directory. **The controller exits after the turn completes.** Press Ctrl-C in either terminal to stop early.
