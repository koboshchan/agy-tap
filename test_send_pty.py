#!/usr/bin/env python3
"""Offline fake-TUI immediate sends and exact native Alt+M input. No model calls."""
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import termios
import unittest
import urllib.request

from test_pty import Terminal

CHILD = r"""
import json,os,select,signal,sys,termios,tty
from pathlib import Path
root=Path(sys.argv[1]).parent
Path(sys.argv[1]).write_text(str(os.getpid()))
saved=termios.tcgetattr(0)
def interrupted(sig,frame):
 print("INTERRUPTED",sig,flush=True)
 raise SystemExit(130)
signal.signal(signal.SIGINT,interrupted)
signal.signal(signal.SIGTERM,interrupted)
raw=b"";done=False;pending=b""
try:
 tty.setraw(0)
 mode=termios.tcgetattr(0);mode[3]|=termios.ISIG
 termios.tcsetattr(0,termios.TCSANOW,mode)
 # Intentionally NEVER display an idle prompt. tap must not depend on it.
 print("\x1b[?2004hBUSY_ORIGINAL_TURN",flush=True)
 while not (root/"stop").exists():
  if not done and (root/"release").exists():
   done=True;print("ORIGINAL_TURN_DONE",flush=True)
  if not select.select([0],[],[],.02)[0]:continue
  data=os.read(0,4096)
  if not data:break
  raw+=data
  (root/"received").write_bytes(raw)
  print("RX "+str(len(raw)),flush=True)
finally:
 termios.tcsetattr(0,termios.TCSANOW,saved)
"""

class SendPTYTests(unittest.TestCase):
 def test_immediate_send_and_exact_force_busy_without_interrupt(self):
  with tempfile.TemporaryDirectory() as tmp:
   root=Path(tmp)
   selected=os.environ.get("AGY_TAP_TEST_BINARY")
   binary=str(Path(selected).resolve()) if selected else str(root/"tap")
   if not selected:subprocess.run(["go","build","-o",binary,"."],check=True)
   terminal=Terminal([binary,"--"],root,child=CHILD)
   try:
    terminal.until(b"BUSY_ORIGINAL_TURN")
    info=json.loads(next((root/"state").glob("*.json")).read_text())
    env=dict(os.environ,AGY_TAP_DIR=str(root/"state"))
    def command(*args):
     r=subprocess.run([binary,*args],env=env,capture_output=True,text=True,timeout=8)
     self.assertEqual(r.returncode,0,r.stderr)
     receipt=json.loads(r.stdout)
     self.assertTrue(receipt["injected"])
     self.assertNotIn("queued",receipt)
    def received():
     p=root/"received"
     return p.read_bytes() if p.exists() else b""
    def expect(data):
     end=time.monotonic()+5
     while received()!=data and time.monotonic()<end:terminal.drain(.02)
     self.assertEqual(received(),data)
    # Ordinary default send goes straight through during the SAME busy turn.
    command("send",str(info["pid"]),"first 世界\nnext")
    first="\x1b[200~first 世界\nnext\x1b[201~\r".encode()
    expect(first)
    # Intended native use is busy + queued message + empty editor. This fake
    # proves only emitted bytes, not agy queue/UI semantics. No extra submit.
    command("force",str(info["pid"]))
    expect(first+b"\x1bm")
    # Deliberately fragmented local Unicode and terminal sequences are forwarded
    # unchanged. No draft tracking, erase, recreation, blocking or local audit.
    local="synthetic私密\x1b[D\x1b[200~draft\ntext\x1b[201~".encode()
    for byte in local:
     os.write(terminal.fd,bytes([byte]))
     terminal.drain(.002)
    expect(first+b"\x1bm"+local)
    command("send",str(info["pid"]),"remote")
    # Accepted shared-editor limitation: existing local text can combine.
    base=first+b"\x1bm"+local+b"\x1b[200~remote\x1b[201~\r"
    expect(base)
    messages=["concurrent-%d 世界\nline"%i for i in range(8)]
    def send(text):command("send",str(info["pid"]),text)
    def force(_):command("force",str(info["pid"]))
    # Physical input competes with whole remote submissions at the same writer.
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:
     jobs=[pool.submit(send,text) for text in messages]
     jobs += [pool.submit(force,i) for i in range(4)]
     # Don't concurrently drain the terminal from multiple threads.
     jobs += [pool.submit(lambda: [os.write(terminal.fd,b"LOCAL") for _ in range(12)])]
     for j in jobs:j.result()
    expected_len=len(base)+sum(len(("\x1b[200~"+s+"\x1b[201~\r").encode()) for s in messages)+8+60
    end=time.monotonic()+5
    while len(received())<expected_len and time.monotonic()<end:terminal.drain(.02)
    raw=received()
    self.assertEqual(len(raw),expected_len)
    remainder=raw[len(base):]
    # Remote frames must remain contiguous even with local key chunks and force.
    for text in messages:
     frame=("\x1b[200~"+text+"\x1b[201~\r").encode()
     self.assertEqual(remainder.count(frame),1)
     remainder=remainder.replace(frame,b"",1)
    self.assertEqual(remainder.count(b"\x1bm"),4)
    remainder=remainder.replace(b"\x1bm",b"")
    self.assertEqual(remainder,b"LOCAL"*12)
    self.assertNotIn(b"\x03",raw)
    self.assertNotIn(b"INTERRUPTED",terminal.output)
    self.assertNotIn(b"ORIGINAL_TURN_DONE",terminal.output)
    (root/"release").touch()
    terminal.until(b"ORIGINAL_TURN_DONE")
    self.assertNotIn(b"INTERRUPTED",terminal.output)
    # No deferred writes after the busy turn completes.
    terminal.drain(.25)
    self.assertEqual(received(),raw)
    req=urllib.request.Request("http://127.0.0.1:%s/status"%info["port"],
                              headers={"Authorization":"Bearer "+info["token"]})
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=5) as r:
     status=json.load(r)
    self.assertFalse({"queued","ready","input_dirty","input_unknown"} & status.keys())
    audit=next((root/"state").glob("*.injections.jsonl")).read_text()
    self.assertNotIn("synthetic",audit)
    self.assertNotIn("draft",audit)
    (root/"stop").touch()
    self.assertEqual(terminal.wait(),0)
    self.assertEqual(termios.tcgetattr(terminal.fd),terminal.before)
    with self.assertRaises(ProcessLookupError):os.kill(int(terminal.pidfile.read_text()),0)
    self.assertFalse(list((root/"state").glob("*.json")))
    print("SEND_PTY_PASS busy default immediate; exact native Alt+M; serialized concurrent"
          " send/force/local; shared draft accepted; original turn completes; restored; no orphans")
   finally:terminal.close()

if __name__=="__main__":unittest.main(verbosity=2)
