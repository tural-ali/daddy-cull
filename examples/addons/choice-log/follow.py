"""Choice log: an addon with no page that works in the background.

It follows Cull's event stream and appends one line of JSON to choices.jsonl
for every choice saved. After a break or a restart it sends the id of the
last choice it wrote, so Cull carries on from there and nothing is missed or
written twice.

    CULL_URL=http://127.0.0.1:8830 python3 follow.py

Python 3.9 or later, nothing to install.
"""

import json
import os
import time
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
CULL = os.environ.get("CULL_URL", "http://127.0.0.1:8830").rstrip("/")
LOG = HERE / "choices.jsonl"
LAST = HERE / "last-event-id"


def key():
    """The addon's key, which Cull writes when the addon is first turned on."""
    try:
        return (HERE / "key").read_text().strip()
    except FileNotFoundError:
        return ""


def follow(secret, last):
    """Reads the stream until it ends, and returns the last id written."""
    headers = {"Authorization": f"Bearer {secret}", "Accept": "text/event-stream"}
    if last:
        headers["Last-Event-ID"] = last
    request = urllib.request.Request(f"{CULL}/api/events", headers=headers)
    with urllib.request.urlopen(request, timeout=60) as stream:
        event, data, ident = "", [], ""
        for raw in stream:
            line = raw.decode("utf-8").rstrip("\n")
            if line.startswith(":"):
                continue  # Cull's keep-alive comment, every 25 seconds.
            if line:
                name, _, value = line.partition(":")
                value = value[1:] if value.startswith(" ") else value
                if name == "event":
                    event = value
                elif name == "data":
                    data.append(value)
                elif name == "id":
                    ident = value
                continue
            # A blank line ends an event.
            if event == "decision":
                with LOG.open("a") as log:
                    log.write(json.dumps(json.loads("\n".join(data))) + "\n")
                last = ident
                LAST.write_text(last)
            elif event == "addons":
                print("An addon was turned on or off, or Cull's addons folder changed.")
            event, data, ident = "", [], ""
    return last


def main():
    last = LAST.read_text().strip() if LAST.exists() else ""
    print(f"Following {CULL}/api/events, writing to {LOG}")
    while True:
        secret = key()
        if not secret:
            print("Turn Choice log on in Cull's Addons page. That writes its key.")
            time.sleep(10)
            continue
        try:
            last = follow(secret, last)
        except urllib.error.HTTPError as error:
            # 403 means the addon was turned off; the loop waits for it to
            # be turned on again.
            body = json.loads(error.read() or b"{}")
            print(f"Cull answered {error.code}: {body.get('error', error.reason)}")
            time.sleep(10)
        except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
            print(f"Cull could not be reached: {error}. Trying again shortly.")
            time.sleep(5)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
