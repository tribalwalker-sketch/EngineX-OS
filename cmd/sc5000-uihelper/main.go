// scale-httpd — on-deck helper that stores and applies the UI scale factor for
// EngineX OS on PRIME GO+ (JP11S), so the choice can be made from the deck's own
// Control Center without a host computer.
//
// It listens on 127.0.0.1 only (the deck's QML talks to loopback, nothing is exposed
// to the network) and does four things:
//
//	GET  /status                 -> current scale + allowed values
//	POST /set?scale=85[&restart=1] -> remember it, write the systemd drop-in,
//	                                   optionally restart engine.service
//	GET  /apply                  -> re-apply the stored value (used at boot)
//	POST /loop?src=&in=&out=&name= -> cut that range out of the track and write a WAV
//	                                   into "Samples from loops" on its volume
//
// Scale lives in two places on purpose:
//
//	/data/sc5000-custom/state/ui-scale                       menu value, e.g. "85"
//	/run/systemd/system/engine.service.d/90-ui-scale.conf     QT_SCALE_FACTOR for Engine
//
// /run is tmpfs, so boot-activate.sh re-runs /apply (or writes the drop-in itself)
// on every boot; /data survives firmware updates and reboots.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	stateFile = "/data/sc5000-custom/state/ui-scale"
	dropInDir = "/run/systemd/system/engine.service.d"
	dropIn    = dropInDir + "/90-ui-scale.conf"
	listen    = "127.0.0.1:8091"
	systemctl = "/usr/bin/systemctl"
	loopBin   = "/data/sc5000-custom/loop/loop2sample"
)

// allowed maps the menu value (what the user sees) to QT_SCALE_FACTOR (what Qt reads).
var allowed = []struct {
	value  string
	factor string
	label  string
}{
	{"100", "1", "100%"},
	{"95", "0.95", "95%"},
	{"90", "0.9", "90%"},
	{"85", "0.85", "85%"},
	{"80", "0.8", "80%"},
	{"75", "0.75", "75%"},
}

func factorFor(value string) (string, bool) {
	for _, a := range allowed {
		if a.value == value {
			return a.factor, true
		}
	}
	return "", false
}

// stored returns the remembered menu value, "" when nothing was chosen yet.
func stored() string {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeDropIn(factor string) error {
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dropInDir, err)
	}
	body := "[Service]\nEnvironment=\"QT_SCALE_FACTOR=" + factor + "\"\n"
	if err := os.WriteFile(dropIn, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dropIn, err)
	}
	return run(systemctl, "daemon-reload")
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func status() map[string]any {
	val := stored()
	factor, ok := factorFor(val)
	if !ok {
		factor = ""
	}
	labels := make([]string, 0, len(allowed))
	for _, a := range allowed {
		labels = append(labels, a.value)
	}
	return map[string]any{
		"scale":   val,
		"factor":  factor,
		"allowed": labels,
		"dropIn":  dropIn,
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/status", "":
		writeJSON(w, status())
	case "/loop":
		if r.Method != http.MethodPost {
			writeJSONErr(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		src, inPos, outPos, name := q.Get("src"), q.Get("in"), q.Get("out"), q.Get("name")
		if src == "" || inPos == "" || outPos == "" {
			writeJSONErr(w, http.StatusBadRequest, "src, in and out are required")
			return
		}
		// Only Engine's own URIs and mounted volumes; never a shell, so spaces are safe.
		if !strings.HasPrefix(src, "net://") && !strings.HasPrefix(src, "/media/") {
			writeJSONErr(w, http.StatusBadRequest, "src must be a net:// URI or a /media path")
			return
		}
		out, err := exec.Command(loopBin, "-src", src, "-in", inPos, "-out", outPos, "-name", name).CombinedOutput()
		text := strings.TrimSpace(string(out))
		log.Printf("loop %q: %s", name, firstLine(text))
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, text)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "output": text})
	case "/apply": // used by boot-activate.sh: re-create the drop-in from /data
		val := stored()
		factor, ok := factorFor(val)
		if !ok {
			writeJSONErr(w, http.StatusBadRequest, "no valid scale stored")
			return
		}
		if err := writeDropIn(factor); err != nil {
			log.Printf("apply: %v", err)
			writeJSONErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("applied scale=%s factor=%s", val, factor)
		writeJSON(w, status())
	case "/set":
		if r.Method != http.MethodPost {
			writeJSONErr(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		val := q.Get("scale")
		factor, ok := factorFor(val)
		if !ok {
			log.Printf("rejected scale=%q", val)
			writeJSONErr(w, http.StatusBadRequest, "scale must be one of 100 95 90 85 80 75")
			return
		}
		if err := writeDropIn(factor); err != nil {
			log.Printf("drop-in: %v", err)
			writeJSONErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := os.WriteFile(stateFile, []byte(val+"\n"), 0o644); err != nil {
			log.Printf("state: %v", err)
			writeJSONErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("scale=%s factor=%s restart=%s", val, factor, q.Get("restart"))
		writeJSON(w, status())
		if q.Get("restart") == "1" {
			// Answer first, then restart Engine: the QML caller waits for this reply.
			go func() {
				time.Sleep(400 * time.Millisecond)
				if err := run(systemctl, "restart", "engine.service"); err != nil {
					log.Printf("restart engine: %v", err)
				}
			}()
		}
	default:
		writeJSONErr(w, http.StatusNotFound, "unknown path")
	}
}

// firstLine keeps log lines short when a helper prints several.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode: %v", err)
	}
}

func writeJSONErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, "{\"error\":%q}\n", msg)
}

func main() {
	log.SetPrefix("scale-httpd: ")
	log.SetFlags(0)
	http.HandleFunc("/", handle)
	log.Printf("listening on http://%s (loopback only), scale=%q", listen, stored())
	if err := http.ListenAndServe(listen, nil); err != nil {
		log.Fatal(err)
	}
}
