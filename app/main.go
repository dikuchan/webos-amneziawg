package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("AmneziaWG", version)
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: amneziawgctl import FILE | start | stop | status | logs | autostart enable|disable | lan open [en|ru]|status|close | routing all|exclude|include|update | boot | check")
		os.Exit(2)
	}
	var err error
	if os.Args[1] == "boot" && len(os.Args) == 2 {
		var kit string
		kit, err = kitDir()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			err = runBoot(ctx, kit)
			cancel()
		}
	} else if len(os.Args) == 2 && os.Args[1] == string(uploadWorker) {
		err = serveUpload()
	} else if len(os.Args) == 2 && os.Args[1] == string(routingWorker) {
		err = runRoutingUpdater()
	} else if len(os.Args) == 3 && os.Args[1] == "routing" && os.Args[2] == "update" {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		err = refreshRouting(ctx, updateManual)
		cancel()
		if err == nil {
			err = printRoutingStatus()
		}
	} else if os.Args[1] == "logs" {
		if len(os.Args) != 2 {
			err = fmt.Errorf("unexpected command arguments")
		} else {
			err = prepareRuntime()
			if err == nil {
				var text string
				text, err = readJournal(journalPath)
				if err == nil {
					fmt.Print(text)
				}
			}
		}
	} else {
		err = withLock(func() error {
			kit, err := kitDir()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			switch os.Args[1] {
			case "import", "start", "stop":
				action := os.Args[1]
				if action == "import" && len(os.Args) != 3 {
					return fmt.Errorf("import requires one config path")
				}
				if action != "import" && len(os.Args) != 2 {
					return fmt.Errorf("unexpected command arguments")
				}
				cancelErr := suppressBoot()
				if cancelErr != nil && action != "stop" {
					return cancelErr
				}
				recordEvent(action + ": requested.")
				var result error
				switch action {
				case "import":
					result = importConfig(os.Args[2])
				case "start":
					result = start(ctx, kit)
				case "stop":
					result = stop(ctx, kit)
				}
				result = errors.Join(result, cancelErr)
				if result != nil {
					recordEvent(action + ": failed: " + result.Error())
				} else {
					recordEvent(action + ": completed.")
				}
				return result
			case "lan":
				if len(os.Args) < 3 || len(os.Args) > 4 {
					return fmt.Errorf("lan requires open, status or close")
				}
				switch os.Args[2] {
				case "open":
					lang := "en"
					if len(os.Args) == 4 {
						lang = os.Args[3]
						if lang != "en" && lang != "ru" {
							return fmt.Errorf("lan language must be en or ru")
						}
					}
					return openUpload(ctx, kit, lang)
				case "status":
					if len(os.Args) != 3 {
						return fmt.Errorf("unexpected lan arguments")
					}
					return printUploadStatus()
				case "close":
					if len(os.Args) != 3 {
						return fmt.Errorf("unexpected lan arguments")
					}
					return closeUpload()
				default:
					return fmt.Errorf("unknown lan command")
				}
			case "routing":
				if len(os.Args) != 3 {
					return fmt.Errorf("routing requires all, exclude, include or update")
				}
				return setRoutingMode(ctx, kit, routingMode(os.Args[2]))
			case "status":
				if len(os.Args) != 2 {
					return fmt.Errorf("unexpected status arguments")
				}
				return status(ctx, kit)
			case "autostart":
				if len(os.Args) != 3 {
					return fmt.Errorf("autostart requires enable or disable")
				}
				switch os.Args[2] {
				case "enable":
					return configureAutostart(kit, bootEnabled)
				case "disable":
					return configureAutostart(kit, bootDisabled)
				default:
					return fmt.Errorf("autostart requires enable or disable")
				}
			case "check":
				for _, name := range []string{
					"amneziawg-go",
					"awg",
				} {
					out, err := command(ctx, kit+"/bin/"+name, "--version")
					if err != nil {
						return err
					}
					fmt.Println(out)
				}
				fmt.Println("AmneziaWG", version, "— binaries execute; no tunnel started.")
				return nil
			default:
				return fmt.Errorf("unknown command")
			}
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
