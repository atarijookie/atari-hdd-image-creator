package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
	"github.com/atarijookie/atari-hdd-image-creator/internal/image"
)

const defaultImageName = "atari.img"

var errCancelled = errors.New("cancelled")

// wizard asks for the same choices as the command line options.
type wizard struct {
	in  *bufio.Reader
	out io.Writer
}

func newWizard(in io.Reader, out io.Writer) *wizard {
	return &wizard{bufio.NewReader(in), out}
}

func (w *wizard) run() error {
	fmt.Fprintln(w.out, "Atari HDD image creator")
	action, err := w.choose("What do you want to do?", []string{
		"Create a new image",
		"Change the driver on an existing image",
	})
	if err != nil {
		return err
	}
	if action == 0 {
		return w.create()
	}
	return w.changeDriver()
}

func (w *wizard) create() error {
	tos, err := w.chooseTOS()
	if err != nil {
		return err
	}
	bus := "acsi"
	if tos.SupportsSCSI {
		choice, err := w.choose("Which bus will the disk be on?", []string{"ACSI", "SCSI"})
		if err != nil {
			return err
		}
		bus = []string{"acsi", "scsi"}[choice]
	}
	size, err := w.askSize(tos)
	if err != nil {
		return err
	}
	name, err := w.chooseDriver()
	if err != nil {
		return err
	}
	plan, err := image.NewPlan(size, tos)
	if err != nil {
		return err
	}
	driver, err := drivers.Load(name, bus)
	if err != nil {
		return err
	}
	fmt.Fprintf(w.out, "\nThis image will have:\n")
	printPlan(w.out, plan, driver.FileName)
	path, force, err := w.askOutput()
	if err != nil {
		return err
	}
	fmt.Fprintln(w.out)
	return buildImage(path, size, tos, name, bus, force, w.out)
}

func (w *wizard) chooseTOS() (image.TOS, error) {
	versions := image.TOSVersions()
	options := make([]string, len(versions))
	for i, tos := range versions {
		options[i] = fmt.Sprintf("TOS %s, up to %d MB", tos.Version, tos.MaxImageMB())
	}
	choice, err := w.choose("Which TOS version will boot this image?", options)
	if err != nil {
		return image.TOS{}, err
	}
	return versions[choice], nil
}

func (w *wizard) chooseDriver() (string, error) {
	catalog := drivers.Catalog()
	options := make([]string, len(catalog))
	for i, driver := range catalog {
		options[i] = fmt.Sprintf("%s %s: %s", driver.Title, driver.Version, driver.Note)
	}
	choice, err := w.choose("Which driver do you want?", options)
	if err != nil {
		return "", err
	}
	return catalog[choice].Name, nil
}

func (w *wizard) askSize(tos image.TOS) (int, error) {
	for {
		answer, err := w.ask(fmt.Sprintf("\nImage size in MB (%d to %d): ", image.MinSizeMB, tos.MaxImageMB()))
		if err != nil {
			return 0, err
		}
		size, err := strconv.Atoi(answer)
		if err == nil && size >= image.MinSizeMB && size <= tos.MaxImageMB() {
			return size, nil
		}
		fmt.Fprintf(w.out, "Enter a whole number from %d to %d.\n", image.MinSizeMB, tos.MaxImageMB())
	}
}

// askOutput returns the output path, and whether it may be overwritten.
func (w *wizard) askOutput() (string, bool, error) {
	for {
		path, err := w.ask(fmt.Sprintf("\nImage file [%s]: ", defaultImageName))
		if err != nil {
			return "", false, err
		}
		if path == "" {
			path = defaultImageName
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, false, nil
		} else if err != nil {
			return "", false, err
		}
		overwrite, err := w.confirm(path + " already exists. Overwrite it? [y/N]: ")
		if err != nil {
			return "", false, err
		}
		if overwrite {
			return path, true, nil
		}
	}
}

func (w *wizard) changeDriver() error {
	path, err := w.askExisting()
	if err != nil {
		return err
	}
	info, err := image.Inspect(path)
	if err != nil {
		return err
	}
	current := info.RootLoader
	if current == "" {
		current = "unknown"
	}
	fmt.Fprintf(w.out, "Current driver: %s\n", current)
	name, err := w.chooseDriver()
	if err != nil {
		return err
	}
	bus := ""
	if name == "icd" && info.RootBus == "" {
		choice, err := w.choose("Which bus is the disk on?", []string{"ACSI", "SCSI"})
		if err != nil {
			return err
		}
		bus = []string{"acsi", "scsi"}[choice]
	}
	proceed, err := w.confirm("Replace the driver? Partitions and other files stay as they are. [y/N]: ")
	if err != nil {
		return err
	}
	if !proceed {
		return errCancelled
	}
	fmt.Fprintln(w.out)
	return changeDriver(path, name, bus, w.out)
}

func (w *wizard) askExisting() (string, error) {
	for {
		path, err := w.ask("\nImage file: ")
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); path != "" && err == nil {
			return path, nil
		}
		fmt.Fprintln(w.out, "File not found.")
	}
}

func (w *wizard) choose(title string, options []string) (int, error) {
	fmt.Fprintf(w.out, "\n%s\n", title)
	for i, option := range options {
		fmt.Fprintf(w.out, "  %d) %s\n", i+1, option)
	}
	for {
		answer, err := w.ask("Choice: ")
		if err != nil {
			return 0, err
		}
		choice, err := strconv.Atoi(answer)
		if err == nil && choice >= 1 && choice <= len(options) {
			return choice - 1, nil
		}
		fmt.Fprintf(w.out, "Enter a number from 1 to %d.\n", len(options))
	}
}

func (w *wizard) confirm(prompt string) (bool, error) {
	answer, err := w.ask(prompt)
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(answer)
	return answer == "y" || answer == "yes", nil
}

// ask reads one line. Input that ends mid-question cancels the wizard.
func (w *wizard) ask(prompt string) (string, error) {
	fmt.Fprint(w.out, prompt)
	line, err := w.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", errCancelled
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}
