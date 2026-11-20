```
 █████╗ ██████╗ ██████╗ ██╗  ██╗
██╔══██╗██╔══██╗██╔══██╗╚██╗██╔╝
███████║██████╔╝██║  ██║ ╚███╔╝ 
██╔══██║██╔══██╗██║  ██║ ██╔██╗ 
██║  ██║██║  ██║██████╔╝██╔╝ ██╗
╚═╝  ╚═╝╚═╝  ╚═╝╚═════╝ ╚═╝  ╚═╝
```

`ardx` compiles and uploads Arduino sketches from the terminal (e.g. the VS Code terminal), so you don't need to copy sketches into the Arduino IDE to upload them.

- [:computer: Installation](#computer-installation)
- [:rocket: Usage](#rocket-usage)
- [:books: Libraries](#books-libraries)
- [:gear: How it works](#gear-how-it-works)

**v0.1.0**

### :computer: Installation

Locally: `git clone git@github.com:5amCurfew/ardx.git`; `make build`

`ardx` drives [`arduino-cli`](https://arduino.github.io/arduino-cli/), the engine behind the Arduino IDE. It is found, in order, from:

1. the `ARDX_ARDUINO_CLI` environment variable
2. `arduino-cli` on your `PATH` (`brew install arduino-cli`)
3. the copy bundled inside the Arduino IDE 2.x (`/Applications/Arduino IDE.app/...`)

So if the Arduino IDE is installed, there is nothing else to set up.

```bash
$ ardx -h
ardx compiles and uploads Arduino sketches from the terminal, using library .zip files from a local lib directory

Usage:
  ardx [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  ports       list serial ports and the Arduinos detected on them
  upload      pick a sketch and an Arduino, then compile and upload

Flags:
  -h, --help      help for ardx
  -v, --version   version for ardx
```

```bash
$ ardx upload -h
Usage:
  ardx upload [PATH_TO_SKETCH] [flags]

Flags:
  -b, --fqbn string      board FQBN, overriding auto-detection (e.g. arduino:avr:nano)
  -h, --help             help for upload
  -l, --lib-dir string   directory of library .zip files (default "lib")
  -p, --port string      port to upload to, skipping the Arduino picker (e.g. /dev/cu.usbmodem1101)
```

### :rocket: Usage

From your project directory run `ardx upload`:

**1. Pick a sketch**: every `.ino` in the current directory, plus `<name>/<name>.ino` sketch folders one level down.

**2. Pick an Arduino**: ports are scanned in the background as soon as `ardx` starts and refresh every 2s, so you can plug a board in while the picker is open.

```
Pick an Arduino 3 port(s), auto-refreshing

▸ ● /dev/cu.usbmodem1101             Arduino UNO (arduino:avr:uno)
  ◐ /dev/cu.usbserial-110            USB serial (CH340), board type unknown
  ○ /dev/cu.Bluetooth-Incoming-Port  Serial Port
```

- `●` green: an Arduino identified by `arduino-cli` (or with an Arduino USB vendor id)
- `◐` yellow: a USB-serial chip (CH340, FTDI, CP210x, PL2303) common on clone boards
- `○` grey: anything else

If the board type can't be identified (typical for clones), a filterable list of installed boards is shown. Type `nano` to narrow it down. Pass `-b arduino:avr:nano` to skip this.

**3. Compile and upload**, with a live stage table:

```
Stage      Status       Took       Detail
------------------------------------------------------------
libraries  [✓] Done     0.0s       1 zip(s) extracted, using TM1637
core       [✓] Done     0.4s       arduino:avr installed
compile    [✓] Done     2.1s       flash 960 B (2%) · ram 9 B (0%)
upload     [✓] Done     3.2s       flashed /dev/cu.usbmodem1101

✓ blink uploaded to /dev/cu.usbmodem1101
```

On failure, the tail of the stage's log is printed. Compile errors point at your original `.ino` file, so they are clickable in VS Code.

Skip the pickers for a fast edit/upload loop:

```bash
ardx upload blink.ino -p /dev/cu.usbmodem1101
```

`ardx ports` prints the port list without the TUI.

### :books: Libraries

Put library `.zip` files (e.g. the "Download ZIP" from a library's GitHub page) in `./lib`, as you would with the Arduino IDE's **Sketch → Include Library → Add .ZIP Library...**:

```
my-project/
├── blink.ino
└── lib/
    ├── TM1637-1.2.0.zip
    └── Keypad.zip
```

Each zip is extracted into `.ardx/libraries/<name>/`. A zip's wrapper folder (e.g. `TM1637-1.2.0/`) is stripped, and the folder is named from `library.properties` the way the Library Manager names it. The extracted libraries are then handed to the compiler. As with the IDE, only libraries whose headers your sketch `#include`s are compiled in, and libraries installed for the Arduino IDE (`~/Documents/Arduino/libraries`) remain available too. Unzipped library folders placed in `lib/` also work.

### :gear: How it works

```
ardx upload
  │
  ├─ pick sketch ─────── *.ino in cwd (+ <name>/<name>.ino)
  ├─ pick arduino ────── arduino-cli board list (background, every 2s)
  ├─ pick board ──────── only if the port didn't identify one (arduino-cli board listall)
  │
  └─ stages
       libraries  extract lib/*.zip → .ardx/libraries; stage loose sketches
       core       arduino-cli core install <vendor:arch> if missing
       compile    arduino-cli compile --build-path .ardx/build/... --libraries .ardx/libraries
       upload     arduino-cli upload --port <port> --input-dir .ardx/build/...
```

- **Loose sketches**: `arduino-cli` requires a sketch to live in a folder of the same name. A `blink.ino` sitting directly in your project is copied, along with its sibling `.h/.cpp/.c` files, into `.ardx/sketches/blink/blink.ino` before compiling. Your file is never modified.
- **Build cache**: builds live in `.ardx/build/<sketch>/<fqbn>/`, so repeat uploads only recompile what changed.
- **Logs**: each stage's output is written to `.ardx/logs/<timestamp>/<stage>.log`.

Add `.ardx/` to your project's `.gitignore`.
