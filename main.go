package main

import (
	"fmt"
	"os"

	lib "github.com/5amCurfew/ardx/lib"
	tea "github.com/charmbracelet/bubbletea"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var version = "0.1.0"

func main() {
	Execute()
}

var (
	libDir string
	port   string
	fqbn   string
)

var rootCmd = &cobra.Command{
	Use:     "ardx",
	Version: version,
	Short:   "ardx - arduino sketch loader",
	Long:    `ardx compiles and uploads Arduino sketches from the terminal, using library .zip files from a local lib directory`,
}

var uploadCmd = &cobra.Command{
	Use:   "upload [PATH_TO_SKETCH]",
	Short: "pick a sketch and an Arduino, then compile and upload",
	Long: `Pick a sketch (.ino) from the current directory, pick the Arduino to upload to
from the detected ports, then compile and upload it.

Libraries are resolved from the .zip files in the lib directory (like the Arduino
IDE's "Add .ZIP Library"), as well as libraries installed for the Arduino IDE.

Arguments:
  PATH_TO_SKETCH   .ino file or sketch folder to upload (default: pick interactively)`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cli, err := lib.FindArduinoCLI()
		if err != nil {
			log.Fatalf("[INIT] %s", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("[INIT] error reading current directory: %s", err)
		}
		opts := lib.LoadOptions{ProjectDir: cwd, LibDir: libDir, Port: port, FQBN: fqbn}
		if len(args) > 0 {
			opts.Sketch = args[0]
		}

		m, err := lib.NewLoadModel(cli, opts)
		if err != nil {
			log.Fatalf("[INIT] %s", err)
		}
		if _, err := tea.NewProgram(m).Run(); err != nil {
			log.Fatalf("[TUI] %s", err)
		}
		if m.Failed() {
			os.Exit(1)
		}
	},
}

var portsCmd = &cobra.Command{
	Use:   "ports",
	Short: "list serial ports and the Arduinos detected on them",
	Long:  `Scan the serial ports and print what is on each, Arduino-detected ports first.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cli, err := lib.FindArduinoCLI()
		if err != nil {
			log.Fatalf("[INIT] %s", err)
		}
		ports, err := cli.ListPorts()
		if err != nil {
			log.Fatalf("[PORTS] %s", err)
		}
		if len(ports) == 0 {
			fmt.Println("no ports found")
			return
		}
		fmt.Print(lib.FormatPorts(ports))
	},
}

func init() {
	uploadCmd.Flags().StringVarP(&libDir, "lib-dir", "l", "lib", "directory of library .zip files")
	uploadCmd.Flags().StringVarP(&port, "port", "p", "", "port to upload to, skipping the Arduino picker (e.g. /dev/cu.usbmodem1101)")
	uploadCmd.Flags().StringVarP(&fqbn, "fqbn", "b", "", "board FQBN, overriding auto-detection (e.g. arduino:avr:nano)")
	rootCmd.AddCommand(uploadCmd, portsCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Errorf("[INIT] error using ardx: %s", err)
		os.Exit(1)
	}
}
