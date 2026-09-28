# Addons

Everything in Cull beyond reviewing dates, finding duplicates and the Bin is an addon.
Screenshots, Saved from social, Shadowed copies, Takeout upgrades, Apple Photos, Immich and Daddy Cull classic come with Cull and can each be turned off on the Addons page.
Anyone can add another as a folder holding an `addon.json`, written in any language, that works through the same API Cull's own pages use.

Turning an addon off takes its pages out of the sidebar, stops its routes and its background work, and refuses its key.
Nothing it did is undone.
Files it moved to the Bin stay there and can be put back, because the Bin belongs to Cull, not to the addon.

The API reference is at `/developers` in Cull, and as OpenAPI 3.1 at `/api/openapi.json`.
The two examples in [`examples/addons`](../examples/addons) are the quickest way in.

## Where addons live

Cull reads its addons folder every second.
It is `state/addons` in the folder Cull is started from, unless `-addons-dir` names another.
Each addon is one folder in it, named as the addon's `id`.
A folder whose name starts with a dot is ignored.

An addon put in the folder shows on the Addons page a moment later, and does nothing until it is turned on.
An addon edited or taken out is followed the same way.

## addon.json

```json
{
  "id": "hello-cull",
  "name": "Hello, Cull",
  "version": "0.1.0",
  "summary": "A page in Cull's sidebar that shows what the library holds.",
  "description": "The smallest addon with a page, to start from.",
  "author": "Your name",
  "homepage": "https://example.com/hello-cull",
  "icon": "extension",
  "pages": [
    {"id": "hello", "label": "Hello", "icon": "extension", "section": "tools", "url": "http://127.0.0.1:8900/"}
  ],
  "permissions": [],
  "needs": ["Node 18 or later, running server.mjs"],
  "work": []
}
```

| Field | Required | What it holds |
| --- | --- | --- |
| `id` | yes | 2 to 40 lower-case letters, digits and hyphens, starting with a letter or digit. It must match the folder's name and must not be the id of one of Cull's own addons. |
| `name` | yes | How the addon is called on the Addons page, in the reference and in error messages. |
| `version` | yes | The addon's own version, in any form. Cull shows it and does not compare it. |
| `summary` | yes | One sentence saying what it is for. |
| `description` | no | More about what it adds and what it needs. |
| `author` | no | Who wrote it. |
| `homepage` | no | Where to read more. |
| `icon` | no | A Material Symbols name from the set Cull ships. Any other is drawn as a puzzle piece. |
| `pages` | no | Pages it adds to the sidebar while it is on. |
| `permissions` | no | What its key may do beyond reading: `review`, `bin`, `delete` or `settings`. |
| `needs` | no | What it needs to work, in words, listed on the Addons page. |
| `work` | no | What it does in the background while it is on, in words. |

A field Cull does not know is an error, so a misspelt field is caught rather than ignored.
A manifest that cannot be read, or breaks one of these rules, shows on the Addons page as a problem with the reason, and the addon cannot be turned on until it is fixed.

### Pages

| Field | What it holds |
| --- | --- |
| `id` | Unique within the addon, lower-case letters, digits and hyphens. |
| `label` | The page's name in the sidebar. |
| `icon` | A Material Symbols name, as for the addon. |
| `section` | Where in the sidebar it goes: `collections`, `sync` or `tools`. |
| `url` | The `http` or `https` address the addon serves the page at. |

The page is at `/addons/<addon>/<page>` in Cull, which shows the `url` inside a frame.
The `url` is opened by the browser, not by Cull, so it must be an address the browser can reach.

## The key

The first time an addon of your own is turned on, Cull writes a file called `key` into its folder, readable only by its owner.
It is never shown on a page or written to a log.
Turning the addon off and on again keeps the same key.
To give an addon a new key, delete `key` and turn the addon off and on again.

Send the key with every request:

```sh
curl -H "Authorization: Bearer $(cat key)" http://127.0.0.1:8830/api/stats
```

Keep the key on the machine the addon runs on.
A page shown in Cull's frame should ask its own server, which holds the key, rather than hold the key in the browser.
`hello-cull` shows how.

## Permissions

Every addon may read: see your library, its dates, choices and settings.
Anything more is asked for in `permissions`, listed on the Addons page, shown again before the addon is first turned on, and checked on every request.

| Permission | What it allows |
| --- | --- |
| `review` | Save choices: keep or remove, favourites, turns and reviewed dates. |
| `bin` | Move files into the Bin and put them back. |
| `delete` | Delete files in the Bin for good. |
| `settings` | Change settings and turn addons on and off. |

The reference at `/developers` gives the exact wording Cull shows, and each route there says which permission it needs.
A permission added to `addon.json` later takes effect once the addon is turned off and on again.

## Answers and errors

Every route is under `/api` and speaks JSON.
A failure is a status of 400 or more with a sentence that can be shown as it is: `{"error": "…"}`.

These come from Cull's guard, before a route runs:

| Status | When |
| --- | --- |
| 401 | The `Authorization` header is not `Bearer <key>`, or Cull does not know the key. |
| 403 | The addon is turned off, cannot be loaded, or has not asked for the permission the route needs. |
| 404 | The route belongs to one of Cull's own addons, and that addon is turned off. |

Cull has no accounts.
Its own pages send no key, so anything that can reach Cull can use it.
Keep Cull on your own network.
A key says which addon is asking, so turning the addon off stops it, and it cannot do more than it asked for.

## Following what happens

`GET /api/events` is a stream of server-sent events, so an addon can follow Cull without asking again and again.

| Event | Data | When |
| --- | --- | --- |
| `catalogue` | `{"generation": n}` | When the stream opens, and whenever the catalogue changes. |
| `decision` | The choice saved: `assetId`, `status`, `favourite`, `previousStatus`, `previousFavourite`, `at` | Each time a choice is saved, with an `id`. |
| `addons` | `{}` | An addon was turned on or off, or the addons folder changed. |

A comment line arrives every 25 seconds to keep the connection open.
After a break, send the id of the last `decision` handled as `Last-Event-ID`, or as `?after=`, and the stream carries on from there, so nothing is missed or handled twice.
`choice-log` shows how.

## Pages in Cull's frame

Cull shows an addon's page in a sandboxed frame that may run scripts, send forms, open pop-ups and start downloads.
A page served from a different address than Cull keeps its own origin, so it can use its own storage and cookies.
A page served from Cull's own address is never given that origin, since it would then be Cull.

When the page loads, Cull posts it a message, to the page's origin only:

```json
{
  "type": "cull:hello",
  "version": 1,
  "addon": "hello-cull",
  "page": "hello",
  "theme": "dark",
  "colors": {"background": "…", "surface": "…", "text": "…", "muted": "…", "line": "…", "accent": "…", "onAccent": "…", "warn": "…", "bad": "…"}
}
```

The same message with the type `cull:theme` follows whenever Cull changes between day and night, so the page can match it.

The page can ask Cull to go to one of Cull's own pages:

```js
parent.postMessage({type: 'cull:navigate', path: '/bin'}, cullOrigin);
```

Cull only listens to its own frame, and only moves to its own pages.
Any other address is ignored.

Check `event.origin` against Cull's address before trusting a message, and send `Content-Security-Policy: frame-ancestors <Cull's address>` with the page so nothing else can frame it.

The frame's head has an "Open on its own" link to the `url`, for a page that works better in a tab of its own.

## Versions

The API's version is `x-cull-api` in `/api/openapi.json`, and every answer carries it in the `X-Cull-API-Version` header.
Routes may be added, and answers may gain fields, within a version, so ignore what you do not know rather than refusing it.
A change that would break a caller gets a new version.
`cull:hello` carries the frame messages' own version.
