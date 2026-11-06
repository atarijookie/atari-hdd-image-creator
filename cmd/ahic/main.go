// Command ahic creates bootable Atari ST and TT hard disk images.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
	"github.com/atarijookie/atari-hdd-image-creator/internal/image"
)

// version is set when building a release.
var version = "dev"

var errICDBus = errors.New("ICD needs a different loader per bus, add --bus acsi or --bus scsi")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ahic:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		if !isTerminal(os.Stdin) {
			return usage()
		}
		return newWizard(os.Stdin, os.Stdout).run()
	}
	switch args[0] {
	case "create":
		return create(args[1:])
	case "install-driver":
		return installDriver(args[1:])
	case "info":
		return showInfo(args[1:])
	case "verify":
		return verify(args[1:])
	case "drivers":
		return listDrivers()
	case "version", "--version":
		fmt.Println("ahic", version)
		return nil
	}
	return usage()
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func usage() error {
	versions := make([]string, 0, len(image.TOSVersions()))
	for _, tos := range image.TOSVersions() {
		versions = append(versions, tos.Version)
	}
	return fmt.Errorf(`usage:
  ahic                 start the interactive wizard
  ahic create --size MB --tos VERSION --driver NAME [--bus BUS] [--force] FILE
  ahic install-driver --driver NAME [--bus BUS] FILE
  ahic info FILE
  ahic verify FILE
  ahic drivers
  ahic version

  --size    image size, %d to %d MB
  --tos     %s
  --driver  %s
  --bus     acsi or scsi, scsi needs TOS 3.0x
  --force   overwrite FILE if it exists`,
		image.MinSizeMB, image.MaxSizeMB, strings.Join(versions, ", "), strings.Join(drivers.Names(), ", "))
}

func create(args []string) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {}
	size := flags.Int("size", 0, "")
	version := flags.String("tos", "", "")
	driverName := flags.String("driver", "", "")
	bus := flags.String("bus", "acsi", "")
	force := flags.Bool("force", false, "")
	if err := flags.Parse(args); err != nil {
		return usage()
	}
	if flags.NArg() != 1 || *size == 0 || *version == "" || *driverName == "" {
		return usage()
	}
	tos, err := image.FindTOS(*version)
	if err != nil {
		return err
	}
	return buildImage(flags.Arg(0), *size, tos, *driverName, *bus, *force, os.Stdout)
}

func buildImage(path string, size int, tos image.TOS, driverName, bus string, force bool, out io.Writer) error {
	if bus == "scsi" && !tos.SupportsSCSI {
		return fmt.Errorf("TOS %s cannot boot from SCSI", tos.Version)
	}
	driver, err := drivers.Load(driverName, bus)
	if err != nil {
		return err
	}
	plan, err := image.NewPlan(size, tos)
	if err != nil {
		return err
	}
	options := image.Options{Path: path, Plan: plan, Driver: driver, Force: force, Now: time.Now()}
	if err := image.Create(options); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d MB for TOS %s, %s on %s\n", path, plan.Sectors/2048, tos.Version, driver.Name, strings.ToUpper(bus))
	printPlan(out, plan, driver.FileName)
	return nil
}

func installDriver(args []string) error {
	flags := flag.NewFlagSet("install-driver", flag.ContinueOnError)
	flags.Usage = func() {}
	driverName := flags.String("driver", "", "")
	bus := flags.String("bus", "", "")
	if err := flags.Parse(args); err != nil {
		return usage()
	}
	if flags.NArg() != 1 || *driverName == "" {
		return usage()
	}
	return changeDriver(flags.Arg(0), *driverName, *bus, os.Stdout)
}

func changeDriver(path, driverName, bus string, out io.Writer) error {
	if bus == "" {
		detected, err := image.DetectBus(path)
		if err != nil {
			return err
		}
		bus = detected
	}
	if bus == "" {
		if driverName == "icd" {
			return errICDBus
		}
		bus = "acsi" // AHDI and CBHD use one loader for both buses
	}
	driver, err := drivers.Load(driverName, bus)
	if err != nil {
		return err
	}
	result, err := image.Install(path, driver, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: installed %s as %s\n", path, driver.Name, driver.FileName)
	if result.Renamed != "" {
		fmt.Fprintf(out, "  kept the changed %s as %s\n", driver.FileName, result.Renamed)
	}
	for _, name := range result.Removed {
		fmt.Fprintf(out, "  removed %s\n", name)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(out, "  warning: %s\n", warning)
	}
	return nil
}

func showInfo(args []string) error {
	if len(args) != 1 {
		return usage()
	}
	info, err := image.Inspect(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("%s: %.1f MB\n", args[0], float64(info.Bytes)/(1<<20))
	loader := info.RootLoader
	if loader == "" {
		loader = "unknown"
	}
	if info.RootBus != "" {
		loader += " (" + strings.ToUpper(info.RootBus) + ")"
	}
	boot := info.BootLoader
	if boot == "" {
		boot = "unknown"
	}
	fmt.Printf("  Root loader: %s\n  Boot loader: %s\n", loader, boot)
	if info.DriverFile != "" {
		if info.DriverBytes > 0 {
			fmt.Printf("  Driver file: %s, %d bytes\n", info.DriverFile, info.DriverBytes)
		} else {
			fmt.Printf("  Driver file: %s is missing\n", info.DriverFile)
		}
	}
	letters := info.DriveLetters()
	for i, partition := range info.Partitions {
		note := ""
		if partition.Bootable() {
			note = "  boot"
		}
		size := float64(partition.Size) / 2048
		switch {
		case partition.Problem != "":
			fmt.Printf("  %s: %s %7.1f MB  %s%s\n", letters[i], partition.Type, size, partition.Problem, note)
		case partition.Mountable():
			fmt.Printf("  %s: %s %7.1f MB  %5d-byte sectors  %.1f MB free%s\n", letters[i], partition.Type, size,
				partition.BPB.SectorSize, float64(partition.FreeBytes)/(1<<20), note)
		default:
			fmt.Printf("  %s: %s %7.1f MB%s\n", letters[i], partition.Type, size, note)
		}
	}
	if info.ICDEntries > 0 {
		fmt.Printf("  %d more partitions in ICD's extended table are not listed\n", info.ICDEntries)
	}
	return nil
}

func verify(args []string) error {
	if len(args) != 1 {
		return usage()
	}
	list, err := image.Verify(args[0])
	if err != nil {
		return err
	}
	labels := map[image.Level]string{image.Pass: "ok", image.Warn: "warning", image.Fail: "FAILED"}
	for _, check := range list {
		if check.Detail == "" {
			fmt.Printf("%-8s %s\n", labels[check.Level], check.Name)
		} else {
			fmt.Printf("%-8s %s: %s\n", labels[check.Level], check.Name, check.Detail)
		}
	}
	if image.Failed(list) {
		return fmt.Errorf("%s failed verification", args[0])
	}
	fmt.Println("Rules passed. This does not prove the image boots.")
	return nil
}

func listDrivers() error {
	for _, driver := range drivers.Catalog() {
		fmt.Printf("%-5s %s %s, installs %s\n      Licence: %s\n      %s\n",
			driver.Name, driver.Title, driver.Version, driver.FileName, driver.Licence, driver.Note)
	}
	return nil
}

func printPlan(out io.Writer, plan *image.Plan, driverFile string) {
	for i, partition := range plan.Partitions {
		note := ""
		if partition.Bootable {
			note = "  boot, " + driverFile
		}
		fmt.Fprintf(out, "  %c: %s %7.1f MB  %5d-byte sectors%s\n",
			'C'+i, partition.Type(), float64(partition.Sectors)/2048, partition.Geometry.LogicalSize, note)
	}
}
