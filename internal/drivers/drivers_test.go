package drivers

import (
	"bytes"
	"testing"
)

func TestEveryDriverLoadsAndIdentifies(t *testing.T) {
	for _, name := range Names() {
		for _, bus := range []string{"acsi", "scsi"} {
			driver, err := Load(name, bus)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(driver.Binary, []byte{0x60, 0x1A}) {
				t.Errorf("%s is not an executable", driver.FileName)
			}
			rootName, rootBus, found, err := IdentifyRoot(driver.Root)
			if err != nil || !found || rootName != name {
				t.Errorf("%s root: got %q, found %v, err %v", name, rootName, found, err)
			}
			if name == "icd" && rootBus != bus {
				t.Errorf("icd root on %s reports %q", bus, rootBus)
			}
			bootName, found, err := IdentifyBoot(driver.Boot)
			if err != nil || !found || bootName != name {
				t.Errorf("%s boot: got %q, found %v, err %v", name, bootName, found, err)
			}
		}
	}
}

func TestAHDIHasTheDelayFix(t *testing.T) {
	driver, err := Load("ahdi", "acsi")
	if err != nil {
		t.Fatal(err)
	}
	fixed := []byte{0xB0, 0xB8, 0x04, 0xBA, 0x6C, 0xFA, 0x20, 0x1F}
	if bytes.Count(driver.Binary, fixed) != 1 {
		t.Error("the bundled AHDI is missing its delay fix")
	}
}

func TestLoadRejectsUnknownNames(t *testing.T) {
	if _, err := Load("hddriver", "acsi"); err == nil {
		t.Error("expected an unknown driver error")
	}
	if _, err := Load("ahdi", "ide"); err == nil {
		t.Error("expected an unknown bus error")
	}
}

func TestDamagedArchiveIsRefused(t *testing.T) {
	if _, err := readArchive([]byte("not a zip")); err == nil {
		t.Error("expected an error")
	}
}
