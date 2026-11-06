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
	out, err := runWizard(t, answer("1", "1", "", "64", "2", path))
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
	out, err := runWizard(t, answer("1", strconv.Itoa(versions), "2", "", "1024", "3", path))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	requireGood(t, path, "icd", "scsi")
}

func bootPartition(t *testing.T, path string) image.PartitionInfo {
	t.Helper()
	info, err := image.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Partitions[0]
}

func TestWizardBootChoices(t *testing.T) {
	last := strconv.Itoa(len(image.TOSVersions()))
	cases := []struct {
		name     string
		answers  []string
		typeName string
		sectors  uint32
	}{
		{"old TOS, Enter keeps GEM", []string{"2", ""}, "GEM", 15 * 2048},
		{"old TOS, BGM default", []string{"2", "2", ""}, "BGM", 128 * 2048},
		{"old TOS, BGM chosen size", []string{"2", "2", "300"}, "BGM", 300 * 2048},
		{"TOS 1.02 stops at 128", []string{"1", "2", "129", "128"}, "BGM", 128 * 2048},
		{"TT default", []string{last, "1", ""}, "BGM", 128 * 2048},
		{"TT largest", []string{last, "1", "512"}, "BGM", 1048368},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "disk.img")
		lines := append([]string{"1"}, c.answers...)
		lines = append(lines, "1000", "1", path)
		if c.answers[0] == "1" {
			lines[len(lines)-3] = "512"
		}
		out, err := runWizard(t, answer(lines...))
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, out)
		}
		requireGood(t, path, "ahdi", "")
		boot := bootPartition(t, path)
		if boot.Type != c.typeName || boot.Size != c.sectors {
			t.Errorf("%s: got %s, %d sectors", c.name, boot.Type, boot.Size)
		}
		warned := strings.Contains(out, "may be safer")
		if warned == strings.HasPrefix(c.name, "TT") {
			t.Errorf("%s: wrong GEM advice in:\n%s", c.name, out)
		}
	}
}

func TestWizardImageSizeFollowsBootSize(t *testing.T) {
	last := strconv.Itoa(len(image.TOSVersions()))
	cases := []struct {
		name    string
		answers []string
		sectors uint32
	}{
		{"Enter after default boot", []string{last, "1", "", ""}, 128 * 2048},
		{"Enter after typed boot", []string{last, "1", "200", ""}, 200 * 2048},
		{"too small, then Enter", []string{last, "1", "200", "199", ""}, 200 * 2048},
		{"largest boot", []string{last, "1", "512", ""}, 512 * 2048},
		{"old TOS BGM", []string{"2", "2", "", ""}, 128 * 2048},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "disk.img")
		lines := append(append([]string{"1"}, c.answers...), "1", path)
		out, err := runWizard(t, answer(lines...))
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, out)
		}
		requireGood(t, path, "ahdi", "")
		info, err := image.Inspect(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Sectors != c.sectors || len(info.Partitions) != 1 {
			t.Errorf("%s: got %d sectors, %d partitions", c.name, info.Sectors, len(info.Partitions))
		}
	}
}

func TestWizardGEMBootKeepsSmallImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	out, err := runWizard(t, answer("1", "2", "", "", "4", "1", path))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Image size in MB (4 to 1024): ") {
		t.Errorf("wrong size prompt:\n%s", out)
	}
	if !strings.Contains(out, "Enter a whole number from 4 to 1024.") {
		t.Errorf("Enter should not pick a size after GEM:\n%s", out)
	}
	requireGood(t, path, "ahdi", "")
}

func TestWizardRepeatsBadAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "st.img")
	out, err := runWizard(t, answer("x", "1", "0", "1", "", "400", "abc", "4", "9", "1", path))
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
	if out, err := runWizard(t, answer("1", "1", "", "4", "1", path, "n", other)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if content, _ := os.ReadFile(path); string(content) != "keep" {
		t.Error("existing file was changed")
	}
	requireGood(t, other, "ahdi", "")
	if out, err := runWizard(t, answer("1", "1", "", "4", "2", path, "y")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	requireGood(t, path, "cbhd", "")
}

func TestWizardChangesDriver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "st.img")
	if out, err := runWizard(t, answer("1", "1", "", "64", "1", path)); err != nil {
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
	if out, err := runWizard(t, answer("1", "1", "", "64", "1", path)); err != nil {
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
