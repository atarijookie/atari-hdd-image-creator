package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atarijookie/atari-hdd-image-creator/internal/image"
)

func answer(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func runWizard(t *testing.T, input string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := newWizard(strings.NewReader(input), &out).run()
	return out.String(), err
}

func requireGood(t *testing.T, path, driver, bus string) {
	t.Helper()
	list, err := image.Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if image.Failed(list) {
		t.Fatalf("verification failed: %+v", list)
	}
	info, err := image.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.RootLoader != driver || (driver == "icd" && info.RootBus != bus) {
		t.Fatalf("got %s on %q", info.RootLoader, info.RootBus)
	}
}

func TestWizardCreatesST(t *testing.T) {
	path := filepath.Join(t.TempDir(), "st.img")
	out, err := runWizard(t, answer("1", "1", "64", "2", path))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "Which bus") {
		t.Error("an ST target must not offer SCSI")
	}
	requireGood(t, path, "cbhd", "")
}

func TestWizardCreatesTTOnSCSI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tt.img")
	versions := len(image.TOSVersions())
	out, err := runWizard(t, answer("1", strconv.Itoa(versions), "2", "1024", "3", path))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	requireGood(t, path, "icd", "scsi")
}

func TestWizardRepeatsBadAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "st.img")
	out, err := runWizard(t, answer("x", "1", "0", "1", "400", "abc", "4", "9", "1", path))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "from 4 to 399") {
		t.Errorf("no size limit shown:\n%s", out)
	}
	requireGood(t, path, "ahdi", "")
}

func TestWizardKeepsExistingImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "st.img")
	other := filepath.Join(dir, "other.img")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runWizard(t, answer("1", "1", "4", "1", path, "n", other)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if content, _ := os.ReadFile(path); string(content) != "keep" {
		t.Error("existing file was changed")
	}
	requireGood(t, other, "ahdi", "")
	if out, err := runWizard(t, answer("1", "1", "4", "2", path, "y")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	requireGood(t, path, "cbhd", "")
}

func TestWizardChangesDriver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "st.img")
	if out, err := runWizard(t, answer("1", "1", "64", "1", path)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := runWizard(t, answer("2", "missing.img", path, "3", "2", "y"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Current driver: ahdi") {
		t.Errorf("current driver not shown:\n%s", out)
	}
	requireGood(t, path, "icd", "scsi")
}

func TestWizardCanBeDeclined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "st.img")
	if out, err := runWizard(t, answer("1", "1", "64", "1", path)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := runWizard(t, answer("2", path, "2", "n")); !errors.Is(err, errCancelled) {
		t.Fatalf("got %v", err)
	}
	requireGood(t, path, "ahdi", "")
}

func TestWizardStopsAtEndOfInput(t *testing.T) {
	if _, err := runWizard(t, answer("1")); !errors.Is(err, errCancelled) {
		t.Fatalf("got %v", err)
	}
}
