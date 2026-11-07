#!/usr/bin/env python3
"""Insert the "Loop to Sample" row into a deck's DeviceTabContent.qml.

Only our own code is added; the file itself stays whatever EngineX installed on that
deck, so nothing derived from Denon's resources ends up in git. The insertion point is
the "WaveformStyle { }," line that every supported version already has, and it refuses
to run twice.

    ./patch_device_tab.py <in.qml> <out.qml>
"""
import sys

ANCHOR = "\t\tWaveformStyle { },\n"
MARKER = "Loop to Sample"

ROW = """
		// Loop to Sample: cuts the deck's active loop and drops a WAV next to the track,
		// in "Samples from loops", which Engine scans on its own. Positions are float samples.
		EntryBase.PrefsButtonEntry {
			text: "Loop to Sample"
			buttonText: "save loop"
			onClicked: {
				var pick = 0, src = "", name = "", inPos = "", outPos = ""
				for (var d = 1; d <= 2 && !pick; ++d) {
					var st = Planck.getProperty("/Engine/Deck" + d + "/Track/CurrentLoopStatus")
					if (!st || !st.translator || String(st.translator.string) !== "3") continue
					var n = Planck.getProperty("/Engine/Deck" + d + "/Track/TrackNetworkPath")
					var f = Planck.getProperty("/Engine/Deck" + d + "/Track/FileName")
					var i = Planck.getProperty("/Engine/Deck" + d + "/Track/CurrentLoopInPosition")
					var o = Planck.getProperty("/Engine/Deck" + d + "/Track/CurrentLoopOutPosition")
					if (!n || !i || !o) continue
					src = String(n.translator.string)
					name = (f && f.translator) ? String(f.translator.string) : ("deck" + d)
					inPos = String(i.translator.value)
					outPos = String(o.translator.value)
					pick = d
				}
				if (!pick) {
					PlanckGUI.views.prompts.post({
						type: Prompt.Info,
						message: "No active loop.\\nSet Loop IN/OUT on a deck first.",
						options: Prompt.Cancel | Prompt.Ok
					})
					return
				}
				var url = "http://127.0.0.1:8091/loop?src=" + encodeURIComponent(src) +
					"&in=" + inPos + "&out=" + outPos + "&name=" + encodeURIComponent(name)
				var x = new XMLHttpRequest()
				x.open("POST", url)
				x.onreadystatechange = function() {
					if (x.readyState !== 4) return
					var msg = "Failed: HTTP " + x.status
					try {
						var j = JSON.parse(x.responseText)
						msg = j.ok ? ("Saved:\\n" + String(j.output).split("\\n").pop()) : ("Error:\\n" + j.error)
					} catch (e) {}
					PlanckGUI.views.prompts.post({
						type: Prompt.Info,
						message: msg + "\\n\\nBrowser -> \\"Samples from loops\\" -> tap a slot.",
						options: Prompt.Cancel | Prompt.Ok
					})
				}
				x.send()
			}
		},
"""


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    src, dst = sys.argv[1], sys.argv[2]
    text = open(src, encoding="utf-8").read()
    if MARKER in text:
        print("already patched, nothing to do")
        return
    if ANCHOR not in text:
        sys.exit(f"anchor {ANCHOR!r} not found - refusing to guess where to insert")
    at = text.index(ANCHOR) + len(ANCHOR)
    open(dst, "w", encoding="utf-8").write(text[:at] + ROW + "\n" + text[at:])
    print(f"patched -> {dst}")


if __name__ == "__main__":
    main()
