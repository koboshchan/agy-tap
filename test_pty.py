#!/usr/bin/env python3
"""Offline Unix PTY regressions. Never launches agy or accesses model services.

Run python3 test_pty.py; AGY_TAP_TEST_BINARY can select a staged release.
Only Python's standard library and the project's Go build are used.
"""
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
import unittest

TIMEOUT = 8
SIZES = [(43, 137), (27, 91), (61, 173), (38, 109)]

CHILD = r"""
import json, os, signal, sys, termios, tty
from pathlib import Path
Path(sys.argv[1]).write_text(str(os.getpid()))
saved = termios.tcgetattr(0)
count = 0
def report():
    size = os.get_terminal_size(0)
    print("SIZE " + json.dumps(dict(rows=size.lines, cols=size.columns, count=count,
          tty=[os.isatty(i) for i in range(3)], pid=os.getpid(),
          pgrp=os.getpgrp(), session=os.getsid(0), foreground=os.tcgetpgrp(0))), flush=True)
def resized(signum, frame):
    global count
    count += 1
    report()
def interrupted(signum, frame):
    raise SystemExit(130)
signal.signal(signal.SIGWINCH, resized)
signal.signal(signal.SIGINT, interrupted)
try:
    tty.setraw(0)
    state = termios.tcgetattr(0)
    state[3] |= termios.ISIG
    termios.tcsetattr(0, termios.TCSANOW, state)
    report()
    while True:
        key = os.read(0, 1)
        if key == b"q":
            break
        if key == b"x":
            print("INPUT_OK", flush=True)
finally:
    termios.tcsetattr(0, termios.TCSANOW, saved)
"""

class Terminal:
    def __init__(self, command, root):
        self.pidfile = root / "child.pid"
        self.output = b""
        self.status = None
        # Set dimensions and take the saved state before forking to avoid a
        # parent/child race with the wrapper's MakeRaw.
        self.fd, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ,
                    struct.pack("HHHH", *SIZES[0], 0, 0))
        self.before = termios.tcgetattr(slave)
        self.pid = os.fork()
        if self.pid == 0:
            os.close(self.fd)
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
            for fd in range(3):
                os.dup2(slave, fd)
            if slave > 2:
                os.close(slave)
            env = dict(os.environ, AGY_TAP_DIR=str(root / "state"))
            os.execve(command[0], command + [sys.executable, "-c", CHILD,
                                           str(self.pidfile)], env)
        os.close(slave)

    def drain(self, seconds=.05):
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            if not select.select([self.fd], [], [], max(0, end-time.monotonic()))[0]:
                return
            try:
                data = os.read(self.fd, 65536)
            except OSError as exc:
                if exc.errno == errno.EIO:
                    return
                raise
            if not data:
                return
            self.output += data

    def until(self, marker):
        end = time.monotonic() + TIMEOUT
        while marker not in self.output and time.monotonic() < end:
            self.drain()
        if marker not in self.output:
            raise AssertionError("missing %r in %r" % (marker, self.output))

    def wait(self):
        end = time.monotonic() + TIMEOUT
        while time.monotonic() < end:
            self.drain()
            pid, status = os.waitpid(self.pid, os.WNOHANG)
            if pid:
                self.status = os.waitstatus_to_exitcode(status)
                return self.status
        raise AssertionError("wrapper did not exit: %r" % self.output)

    def close(self):
        # Clean only this test's exact PIDs, including a separately owned child
        # session if an assertion fails.
        if self.pidfile.exists():
            child = int(self.pidfile.read_text())
            try:
                os.kill(child, signal.SIGKILL)
            except ProcessLookupError:
                pass
        if self.status is None:
            try:
                os.kill(self.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            os.waitpid(self.pid, 0)
        os.close(self.fd)


class TerminalTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.tmp.name)
        selected = os.environ.get("AGY_TAP_TEST_BINARY")
        cls.binary = str(Path(selected).resolve()) if selected else str(cls.root / "agy-tap")
        if not selected:
            subprocess.run(["go", "build", "-o", cls.binary, "."], check=True)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def exercise(self, kind, interrupt):
        with tempfile.TemporaryDirectory(dir=self.root) as tmp:
            # A real intermediate exec wrapper exercises the same inheritance
            # boundary as an outer CLI without credentials or a remote server.
            command = [self.binary, "--"]
            if kind == "nested":
                shim = Path(tmp) / "shim.py"
                shim.write_text("import os,sys; os.execv(sys.argv[1],sys.argv[1:])")
                command = [sys.executable, str(shim), *command]
            if kind == "direct":
                command = []
            if not command:
                # Terminal appends the fake TUI executable and arguments.
                command = [sys.executable, "-c",
                           "import os,sys; os.execv(sys.argv[1],sys.argv[1:])"]
            terminal = Terminal(command, Path(tmp))
            try:
                for count, (rows, cols) in enumerate(SIZES):
                    if count:
                        fcntl.ioctl(terminal.fd, termios.TIOCSWINSZ,
                                    struct.pack("HHHH", rows, cols, 0, 0))
                    terminal.until(('"count": %d' % count).encode())
                    states = [json.loads(line.split(b"SIZE ", 1)[1])
                              for line in terminal.output.splitlines() if b"SIZE " in line]
                    state = states[-1]
                    self.assertEqual((state["rows"], state["cols"]), (rows, cols), state)
                    self.assertEqual(state["count"], count, state)
                    self.assertEqual(state["tty"], [True]*3, state)
                    self.assertEqual(state["foreground"], state["pgrp"], state)
                    if kind != "direct":
                        self.assertEqual(state["pid"], state["session"], state)
                        self.assertEqual(state["pid"], state["pgrp"], state)
                        self.assertNotEqual(state["pgrp"], terminal.pid, state)
                    # Catch delayed duplicate delivery before the next resize.
                    terminal.drain(.35)
                    self.assertEqual(terminal.output.count(b"SIZE "), count+1,
                                     terminal.output)
                os.write(terminal.fd, b"x")
                terminal.until(b"INPUT_OK")
                os.write(terminal.fd, b"\x03" if interrupt else b"q")
                expected = (130 if kind == "direct" else 1) if interrupt else 0
                self.assertEqual(terminal.wait(), expected, terminal.output)
                self.assertEqual(termios.tcgetattr(terminal.fd), terminal.before)
                with self.assertRaises(ProcessLookupError):
                    os.kill(int(terminal.pidfile.read_text()), 0)
                self.assertFalse(list((Path(tmp)/"state").glob("*.json")))
                print("PTY_PASS", kind, "interrupt" if interrupt else "normal",
                      "initial+3resizes correct-at-signal foreground-owned restored no-orphans")
            finally:
                terminal.close()

    def test_direct_fake_tui(self):
        for interrupt in (False, True):
            with self.subTest(interrupt=interrupt):
                self.exercise("direct", interrupt)

    def test_tap_fake_tui(self):
        for interrupt in (False, True):
            with self.subTest(interrupt=interrupt):
                self.exercise("tap", interrupt)

    def test_nested_fake_tui(self):
        for interrupt in (False, True):
            with self.subTest(interrupt=interrupt):
                self.exercise("nested", interrupt)


if __name__ == "__main__":
    unittest.main(verbosity=2)
