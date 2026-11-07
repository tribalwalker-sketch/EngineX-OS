# Local additions / 本地附加功能

Two features we run on our own decks and keep here so they survive a re-flash. Neither is
part of the release bundles, and neither touches a signed partition: everything lives under
`/data/sc5000-custom`.

## Loop to Sample

Settings -> Device -> **Loop to Sample** takes the loop that is currently active on either
deck and writes it as a WAV into `Samples from loops` on the same volume. Engine scans new
files by itself, so one tap in the browser puts it into a sampler slot.

| Piece | Path on the deck | Source |
|---|---|---|
| Cutter | `/data/sc5000-custom/loop/loop2sample` | [`tools/loop-to-sample/main.go`](../tools/loop-to-sample/main.go) |
| Helper service (loopback :8091) | `/data/sc5000-custom/scale/scale-httpd` | [`cmd/sc5000-uihelper`](../cmd/sc5000-uihelper) |
| Control Center row | `ui/packs/en/.../DeviceTabContent.qml` | inserted by [`patch_device_tab.py`](../tools/loop-to-sample/patch_device_tab.py) |

Deploy (idempotent, safe to re-run after an update):

```sh
DECK=root@192.168.88.32 KEY=~/.ssh/deck_ed25519 ./tools/loop-to-sample/deploy.sh
```

What it reads from Engine: `Track/CurrentLoopInPosition` and `...OutPosition` (float samples at
the track's own rate, see `Track/SampleRate`), `Track/CurrentLoopStatus == 3` for "a loop is
active", and `Track/TrackNetworkPath` (`net://<db-uuid>/<volume>/...`) for the file.

Known limits:

- **wav and mp3 only** so far; anything else exits with a clear message.
- MP3 is decoded from the start of the file (go-mp3 has no seeking), so expect 10-20 s on a
  full-length track before the dialog appears.
- The row lives in `ui/packs`, which an EngineX update replaces, and the service is started by
  our own boot hook inside `bundle/`, which an update also rewrites. Re-run `deploy.sh` after
  updating; it detects what is already there.

Why this is not simply upstream code: `/Engine/Sampler<N>/Load` cannot be written from QML (a
URL write is ignored, a string write raises a QML `TypeError`), and the pad bank has no readable
on-disk form, so the last step - putting the file into a slot - still needs one human tap. See
the feature request for the engine-side hook that would remove it.

## UI Scale

Settings -> Device -> **UI Scale** (100 / 95 / 90 / 85 / 80 / 75 %) sets `QT_SCALE_FACTOR` for
Engine through `/data/sc5000-custom/ui-scale.env`. The upstream form of this is PR #117; the
helper service above carries it on decks that run an older bundle.
