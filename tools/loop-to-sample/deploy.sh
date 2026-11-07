#!/bin/sh
# Deploy the two local additions (UI Scale + Loop to Sample) to a deck over SSH.
# Everything lands in /data/sc5000-custom; nothing signed is touched, and every step
# checks whether it already ran, so re-running this after an update is safe.
#
#   DECK=root@192.168.88.32 KEY=~/.ssh/deck_ed25519 ./deploy.sh
set -eu

# DECK  - ssh target of the deck
# KEY   - private key that is authorized on it (never commit it)
# UNIT  - systemd unit our boot hook starts for the helper service
DECK=${DECK:-root@192.168.88.32}
KEY=${KEY:-$HOME/.ssh/deck_ed25519}
UNIT=${UNIT:-jekka-scale-httpd}
REPO=$(cd "$(dirname "$0")/../.." && pwd)
QML=/data/sc5000-custom/ui/packs/en/Skins/Embedded/Views/ControlCenter/Tabs/Preferences/DeviceTabContent.qml
SSH="ssh -o StrictHostKeyChecking=no -i $KEY $DECK"
if command -v shasum >/dev/null; then SHA="shasum -a 256"; else SHA="sha256sum"; fi

echo "== 1. build (linux/arm v7, static) =="
cd "$REPO"
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
	-o /tmp/loop2sample ./tools/loop-to-sample
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
	-o /tmp/uihelper ./cmd/sc5000-uihelper

echo "== 2. binaries =="
$SSH 'mkdir -p /data/sc5000-custom/loop /data/sc5000-custom/scale'
$SSH "cat > /data/sc5000-custom/loop/loop2sample" < /tmp/loop2sample
$SSH "cat > /data/sc5000-custom/scale/scale-httpd.new" < /tmp/uihelper
$SSH 'chmod +x /data/sc5000-custom/loop/loop2sample;
      mv /data/sc5000-custom/scale/scale-httpd.new /data/sc5000-custom/scale/scale-httpd;
      chmod +x /data/sc5000-custom/scale/scale-httpd'

echo "== 3. Control Center rows =="
$SSH "cat $QML" > /tmp/DeviceTabContent.current.qml || { echo "cannot read $QML"; exit 1; }
if grep -q "Loop to Sample" /tmp/DeviceTabContent.current.qml; then
	echo "   already patched, skipping"
else
	python3 "$REPO/tools/loop-to-sample/patch_device_tab.py" /tmp/DeviceTabContent.current.qml /tmp/DeviceTabContent.patched.qml
	$SHA /tmp/DeviceTabContent.current.qml | awk '{print "   before:", $1}'
	$SSH "cp $QML $QML.pre-loopsave.bak; cat > $QML" < /tmp/DeviceTabContent.patched.qml
	$SHA /tmp/DeviceTabContent.patched.qml | awk '{print "   after: ", $1}'
fi

echo "== 4. restart =="
$SSH "systemctl restart $UNIT.service 2>/dev/null ||
      (nohup /data/sc5000-custom/scale/scale-httpd >/tmp/uihelper.log 2>&1 &) ;
      sleep 1; systemctl is-active $UNIT.service 2>/dev/null || pgrep -f scale-httpd"
$SSH 'systemctl restart engine.service; sleep 20; systemctl is-active engine.service'

echo
echo "Settings -> Device should now show UI Scale and Loop to Sample."
echo "Rollback of the QML row: $SSH \"mv $QML.pre-loopsave.bak $QML && systemctl restart engine.service\""
