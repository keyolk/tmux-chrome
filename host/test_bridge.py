"""Run with: python3 -m unittest discover -s host"""

import json
import os
import socket
import tempfile
import threading
import time
import unittest

import bridge


def read_line(sock):
    data = b""
    while not data.endswith(b"\n"):
        chunk = sock.recv(4096)
        if not chunk:
            break
        data += chunk
    return json.loads(data.decode())


class SubscribeTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        bridge.SOCKET_PATH = os.path.join(self.dir, "b.sock")
        threading.Thread(target=bridge.socket_server, daemon=True).start()
        for _ in range(100):
            if os.path.exists(bridge.SOCKET_PATH):
                break
            time.sleep(0.01)

    def subscribe(self):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(2)
        s.connect(bridge.SOCKET_PATH)
        s.sendall(b'{"type":"subscribe","id":"t1"}\n')
        self.assertEqual(read_line(s)["event"], "subscribed")
        return s

    def wait_subscribers(self, n):
        for _ in range(100):
            with bridge._subscribers_lock:
                if len(bridge._subscribers) == n:
                    return
            time.sleep(0.01)
        self.fail(f"expected {n} subscribers")

    def test_events_reach_every_subscriber(self):
        a, b = self.subscribe(), self.subscribe()
        self.wait_subscribers(2)
        bridge.handle_chrome_response({"event": "changed", "seq": 7, "epoch": "e"})
        self.assertEqual(read_line(a)["seq"], 7)
        self.assertEqual(read_line(b)["seq"], 7)
        a.close()
        b.close()
        self.wait_subscribers(0)

    def test_responses_with_id_are_not_broadcast(self):
        s = self.subscribe()
        self.wait_subscribers(1)
        bridge.handle_chrome_response({"id": "x", "success": True})
        bridge.handle_chrome_response({"event": "changed", "seq": 1})
        self.assertEqual(read_line(s)["event"], "changed")
        s.close()
        self.wait_subscribers(0)

    def test_ping_when_idle(self):
        old = bridge.PING_INTERVAL
        bridge.PING_INTERVAL = 0.05
        try:
            s = self.subscribe()
            self.assertEqual(read_line(s)["event"], "ping")
            s.close()
            self.wait_subscribers(0)
        finally:
            bridge.PING_INTERVAL = old


if __name__ == "__main__":
    unittest.main()
