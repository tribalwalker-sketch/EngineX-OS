// loop2sample cuts the active loop of a loaded track out of its source file and
// writes it as a WAV into the sampler folder, where Engine picks it up on its own.
//
//	loop2sample -src <net://… or /media/… path> -in 6000878.71 -out 6171588.39 \
//	            -name "SONARFACE - Release Yourself" [-outdir DIR] [-dry]
//
// Positions are float samples at the track's own sample rate, which is what
// /Engine/Deck<N>/Track/CurrentLoop{In,Out}Position reports.
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	mp3 "github.com/hajimehoshi/go-mp3"
)

const defaultFolder = "Samples from loops"

func main() {
	var (
		src    = flag.String("src", "", "source: net://<uuid>/<volume>/… or a plain path")
		inPos  = flag.Float64("in", -1, "loop in, float samples")
		outPos = flag.Float64("out", -1, "loop out, float samples")
		name   = flag.String("name", "", "label used for the output file name")
		outdir = flag.String("outdir", "", "output directory (default: <volume>/"+defaultFolder+")")
		dry    = flag.Bool("dry", false, "resolve and print, write nothing")
	)
	flag.Parse()

	if *src == "" || *inPos < 0 || *outPos <= *inPos {
		fmt.Fprintln(os.Stderr, "usage: loop2sample -src PATH -in N -out M -name LABEL [-outdir DIR] [-dry]")
		os.Exit(2)
	}

	realPath, volumeRoot, err := resolve(*src)
	if err != nil {
		fail("resolve %q: %v", *src, err)
	}
	fmt.Printf("source : %s\nvolume : %s\n", realPath, volumeRoot)

	dstDir := *outdir
	if dstDir == "" {
		dstDir = filepath.Join(volumeRoot, defaultFolder)
	}

	switch strings.ToLower(filepath.Ext(realPath)) {
	case ".wav", ".wave":
		runWAV(realPath, dstDir, *name, int64(*inPos), int64(*outPos), *dry)
	case ".mp3":
		runMP3(realPath, dstDir, *name, int64(*inPos), int64(*outPos), *dry)
	default:
		fail("format %s is not supported yet (wav and mp3 are)", filepath.Ext(realPath))
	}
}

// resolve turns Engine's net:// URI into a path we can open. The first segment after
// the database UUID is the volume's display name ("DENON-DJ (SD)"), which matches the
// mount point under /media once the suffix is dropped; if that misses, every mounted
// volume is tried against the remaining relative path.
func resolve(src string) (path, volumeRoot string, err error) {
	if !strings.HasPrefix(src, "net://") {
		st, err := os.Stat(src)
		if err != nil {
			return "", "", err
		}
		if st.IsDir() {
			return "", "", errors.New("is a directory")
		}
		return src, volumeRootOf(src), nil
	}
	rest := strings.TrimPrefix(src, "net://")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 3 {
		return "", "", errors.New("unexpected net:// layout")
	}
	label, rel := parts[1], parts[2]
	candidates := []string{filepath.Join("/media", cleanLabel(label))}
	if vols, err := filepath.Glob("/media/*"); err == nil {
		for _, v := range vols {
			if !strings.HasSuffix(v, cleanLabel(label)) && v != candidates[0] {
				candidates = append(candidates, v)
			}
		}
	}
	for _, c := range candidates {
		p := filepath.Join(c, rel)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, c, nil
		}
	}
	return "", "", fmt.Errorf("not found under /media (label %q)", label)
}

var labelSuffix = regexp.MustCompile(`(?i)\s*\((sd|usb|drive|disk)\)\s*$`)

func cleanLabel(label string) string {
	if i := strings.LastIndexByte(label, ' '); i > 0 && labelSuffix.MatchString(label[i:]) {
		return label[:i]
	}
	return label
}

// volumeRootOf walks up from a file path to the /media/<volume> component.
func volumeRootOf(path string) string {
	p := filepath.Clean(path)
	for p != "/" && !strings.HasPrefix(p, "/media/") {
		p = filepath.Dir(p)
	}
	if strings.HasPrefix(p, "/media/") {
		return p
	}
	return filepath.Dir(filepath.Dir(p))
}

func runWAV(src, dstDir, name string, in, out int64, dry bool) {
	f, err := os.Open(src)
	if err != nil {
		fail("open: %v", err)
	}
	defer f.Close()

	var hdr [44]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		fail("wav header: %v", err)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		fail("not RIFF/WAVE")
	}
	rate := int64(binary.LittleEndian.Uint32(hdr[24:28]))
	align := int64(binary.LittleEndian.Uint16(hdr[32:34])) // bytes per frame
	if align == 0 {
		fail("wav: zero block align")
	}
	dataOff, dataSize := findData(&hdr, f)

	start := dataOff + in*align
	count := (out - in) * align
	if start < dataOff || start+count > dataOff+dataSize {
		fail("wav: loop [%d,%d) outside the file (%d frames available)", in, out, dataSize/align)
	}
	buf := make([]byte, count)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		fail("read: %v", err)
	}
	fmt.Printf("wav    : rate=%d align=%d frames=%d bytes=%d\n", rate, align, out-in, count)
	if dry {
		fmt.Println("dry run: nothing written")
		return
	}
	writeOut(dstDir, name, buf, rate, int(align))
}

// findData locates the data chunk; fmt is assumed to be the canonical 16-byte one.
func findData(hdr *[44]byte, f *os.File) (int64, int64) {
	if string(hdr[12:16]) == "fmt " && string(hdr[36:40]) == "data" {
		return 44, int64(binary.LittleEndian.Uint32(hdr[40:44]))
	}
	off := int64(12)
	for {
		var c [8]byte
		if _, err := f.ReadAt(c[:], off); err != nil {
			fail("wav: data chunk not found")
		}
		size := int64(binary.LittleEndian.Uint32(c[4:8]))
		if string(c[0:4]) == "data" {
			return off + 8, size
		}
		off += 8 + size + size%2
	}
}

func runMP3(src, dstDir, name string, in, out int64, dry bool) {
	f, err := os.Open(src)
	if err != nil {
		fail("open: %v", err)
	}
	defer f.Close()
	br := bufio.NewReader(f)
	if err := skipID3(br); err != nil {
		fail("mp3 id3: %v", err)
	}
	dec, err := mp3.NewDecoder(br)
	if err != nil {
		fail("mp3 decode: %v", err)
	}

	rate := int64(dec.SampleRate())
	const frame = 2 * 2 // stereo int16 LE
	skip := in * frame
	if skip > 0 {
		if _, err := io.CopyN(io.Discard, dec, skip); err != nil && err != io.EOF {
			fail("mp3 seek: %v", err)
		}
	}
	want := (out - in) * frame
	buf := make([]byte, want)
	n, err := io.ReadFull(dec, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		fail("mp3 read: %v", err)
	}
	buf = buf[:n]
	fmt.Printf("mp3    : rate=%d frames=%d bytes=%d\n", rate, out-in, n)
	if dry {
		fmt.Println("dry run: nothing written")
		return
	}
	buf = buf[:len(buf)-len(buf)%frame]
	writeOut(dstDir, name, buf, rate, frame)
}

// skipID3 steps over an ID3v2 tag, which go-mp3 does not handle on its own.
func skipID3(br *bufio.Reader) error {
	magic, err := br.Peek(10) // Peek leaves the stream untouched
	if err != nil {
		return err
	}
	if string(magic[0:3]) != "ID3" {
		return nil
	}
	size := uint32(magic[6]&0x7f)<<21 | uint32(magic[7]&0x7f)<<14 | uint32(magic[8]&0x7f)<<7 | uint32(magic[9]&0x7f)
	_, err = io.CopyN(io.Discard, br, int64(size))
	return err
}

func writeOut(dir, label string, pcm []byte, rate int64, align int) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("mkdir %s: %v", dir, err)
	}
	frames := int64(len(pcm) / align)
	dst := filepath.Join(dir, fmt.Sprintf("%s.wav", sampleName(label, float64(frames)/float64(rate))))
	tmp := dst + ".part"
	w, err := os.Create(tmp)
	if err != nil {
		fail("create: %v", err)
	}
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+len(pcm)))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16)
	binary.LittleEndian.PutUint16(hdr[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:24], uint16(align/2))
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(rate*int64(align)))
	binary.LittleEndian.PutUint16(hdr[32:34], uint16(align))
	binary.LittleEndian.PutUint16(hdr[34:36], 16)
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(len(pcm)))
	if _, err := w.Write(hdr); err == nil {
		_, err = w.Write(pcm)
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		fail("write: %v", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		fail("rename: %v", err)
	}
	fmt.Printf("written : %s (%.2f s, %d Hz)\n", dst, float64(frames)/float64(rate), rate)
}

var badName = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

func sampleName(label string, seconds float64) string {
	s := strings.TrimSpace(badName.ReplaceAllString(label, " "))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 72 {
		s = s[:72]
	}
	if s == "" {
		s = "loop"
	}
	return fmt.Sprintf("LOOP - %s - %ss", s, strconv.FormatFloat(seconds, 'f', 2, 64))
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+f+"\n", a...)
	os.Exit(1)
}
