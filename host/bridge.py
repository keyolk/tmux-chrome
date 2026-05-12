#!/usr/bin/env python3
"""
Native Messaging Host for tmux-Chrome Tab Group Bridge.

Main thread: reads from Chrome (stdin), keeps connection alive.
Background thread: Unix socket server that accepts commands from tmux.sh.

Protocol:
- Chrome native messaging: 4-byte little-endian length prefix + JSON on stdin/stdout.
- Unix socket: newline-delimited JSON. Client sends request, reads response, disconnects.
"""

import json
import os
import socket
import struct
import sys
import threading

SOCKET_PATH = "/tmp/tmux-chrome-bridge.sock"


def read_native_message():
    """Read a native messaging protocol message from stdin."""
    raw_length = sys.stdin.buffer.read(4)
    if not raw_length or len(raw_length) < 4:
        return None
    length = struct.unpack("<I", raw_length)[0]
    if length > 1024 * 1024:  # 1MB safety limit
        return None
    data = sys.stdin.buffer.read(length)
    if len(data) < length:
        return None
    return json.loads(data.decode("utf-8"))


def send_native_message(msg):
    """Send a native messaging protocol message to stdout (Chrome)."""
    encoded = json.dumps(msg).encode("utf-8")
    sys.stdout.buffer.write(struct.pack("<I", len(encoded)))
    sys.stdout.buffer.write(encoded)
    sys.stdout.buffer.flush()


# Pending response tracking: request_id -> threading.Event + response data
_pending = {}
_pending_lock = threading.Lock()


def send_and_wait(msg, timeout=5.0):
    """Send message to Chrome and wait for response."""
    request_id = msg.get("id")
    if not request_id:
        return None

    event = threading.Event()
    with _pending_lock:
        _pending[request_id] = {"event": event, "response": None}

    send_native_message(msg)
    event.wait(timeout=timeout)

    with _pending_lock:
        entry = _pending.pop(request_id, None)

    if entry and entry["response"] is not None:
        return entry["response"]
    return {"id": request_id, "success": False, "error": "Timeout waiting for Chrome response"}


def handle_chrome_response(msg):
    """Handle a response from Chrome extension."""
    request_id = msg.get("id")
    if not request_id:
        return

    with _pending_lock:
        entry = _pending.get(request_id)
        if entry:
            entry["response"] = msg
            entry["event"].set()


def socket_server():
    """Unix socket server accepting commands from tmux CLI."""
    if os.path.exists(SOCKET_PATH):
        os.unlink(SOCKET_PATH)

    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.bind(SOCKET_PATH)
    os.chmod(SOCKET_PATH, 0o600)
    sock.listen(5)
    sock.settimeout(1.0)  # Allow periodic checking

    while True:
        try:
            conn, _ = sock.accept()
        except socket.timeout:
            continue
        except OSError:
            break

        threading.Thread(target=handle_client, args=(conn,), daemon=True).start()


def handle_client(conn):
    """Handle a single socket client connection."""
    try:
        conn.settimeout(10.0)
        data = b""
        while True:
            chunk = conn.recv(4096)
            if not chunk:
                break
            data += chunk
            if b"\n" in data:
                break

        line = data.decode("utf-8").strip()
        if not line:
            return

        msg = json.loads(line)

        # Forward to Chrome and wait for response
        response = send_and_wait(msg)

        # Send response back to client
        response_bytes = (json.dumps(response) + "\n").encode("utf-8")
        conn.sendall(response_bytes)

    except Exception as e:
        try:
            error_response = json.dumps({"success": False, "error": str(e)}) + "\n"
            conn.sendall(error_response.encode("utf-8"))
        except Exception:
            pass
    finally:
        conn.close()


def main():
    # Start socket server in background
    server_thread = threading.Thread(target=socket_server, daemon=True)
    server_thread.start()

    # Main loop: read from Chrome stdin to keep connection alive
    # and dispatch responses to waiting clients
    while True:
        try:
            msg = read_native_message()
            if msg is None:
                break
            handle_chrome_response(msg)
        except Exception:
            break

    # Cleanup
    try:
        os.unlink(SOCKET_PATH)
    except OSError:
        pass


if __name__ == "__main__":
    main()
