# Example addons

Two small addons to start from.
Neither needs anything installed beyond its language.
[`docs/ADDONS.md`](../../docs/ADDONS.md) says how addons work.

## hello-cull

A page in Cull's sidebar that shows what the library holds, with a button that moves Cull to the Bin.
It shows the three things most addons with a page need: keeping the key on the server, matching Cull's theme, and asking Cull to go somewhere.

1. Copy the `hello-cull` folder into Cull's addons folder, `state/addons` by default.
2. Start its server, which needs Node 18 or later:

   ```sh
   CULL_URL=http://127.0.0.1:8830 node server.mjs
   ```

3. Open Addons in Cull and turn Hello, Cull on.
   Hello appears under Tools.

`CULL_URL` is Cull's address as the browser sees it, since the page only listens to messages from there.
`PORT` and `HOST` say where the addon listens, `127.0.0.1:8900` by default, and the `url` in `addon.json` must match.

## choice-log

An addon with no page that works in the background.
It follows Cull's event stream and appends each choice to `choices.jsonl` as it is saved.
After a break or a restart it carries on from the last choice it wrote, so nothing is missed or written twice.

1. Copy the `choice-log` folder into Cull's addons folder.
2. Open Addons in Cull and turn Choice log on, which writes its key.
3. Start it, which needs Python 3.9 or later:

   ```sh
   CULL_URL=http://127.0.0.1:8830 python3 follow.py
   ```

Turned off in Cull, it waits and carries on when it is turned on again.
