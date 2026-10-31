package gui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/ubavic/bas-celik/v2/document"
	"github.com/ubavic/bas-celik/v2/internal/logger"
)

func saveXlsx(doc document.Document) func() {
	return func() {
		if doc == nil {
			return
		}

		excel, fileName, err := doc.BuildExcel()
		if err != nil {
			setStatus("error.generatingXlsx", fmt.Errorf("generating Xlsx: %w", err))
			return
		}

		fDialog := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
			if err != nil {
				setStatus("error.writingXlsx", fmt.Errorf("writing xlsx: %w", err))
				return
			}
			if w == nil {
				return
			}

			saveLastUsedDirectory(w.URI())

			_, err = w.Write(excel)
			if err != nil {
				setStatus("error.writingXlsx", fmt.Errorf("writing xlsx: %w", err))
				return
			}

			err = w.Close()
			if err != nil {
				setStatus("error.writingXlsx", fmt.Errorf("writing xlsx: %w", err))
				return
			}

			setStatus("ui.xlsxSaved", nil)
		}, state.window)

		fDialog.SetFilter(storage.NewExtensionFileFilter([]string{".xlsx"}))
		fDialog.SetFileName(fileName)
		fDialog.SetView(dialog.ListView)

		lastUsedDirectoryURI := getLastUsedDirectory()
		if lastUsedDirectoryURI != nil {
			fDialog.SetLocation(lastUsedDirectoryURI)
		}

		fDialog.Show()
	}
}

func saveLastUsedDirectory(uri fyne.URI) {
	directoryPath := filepath.Dir(uri.Path())
	if directoryPath == "." || directoryPath == "" {
		return
	}

	preferences := state.app.Preferences()
	preferences.SetString(lastUsedDirectoryKey, directoryPath)
}

func getLastUsedDirectory() fyne.ListableURI {
	preferences := state.app.Preferences()
	lastUsedDirectory := preferences.String(lastUsedDirectoryKey)
	if lastUsedDirectory == "" {
		return nil
	}

	fileURI := storage.NewFileURI(lastUsedDirectory)
	listableURI, err := storage.ListerForURI(fileURI)
	if err != nil {
		logger.Error(err)
		return nil
	}

	return listableURI
}

// getSystemPrinters pronalazi sve dostupne štampače na sistemu
func getSystemPrinters() ([]string, string) {
	printersMap := make(map[string]bool)
	defaultPrinter := ""

	// 1. Očitavanje aktivnih štampača preko lpstat -a
	cmd := exec.Command("lpstat", "-a")
	if out, err := cmd.Output(); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) > 0 {
				printersMap[fields[0]] = true
			}
		}
	}

	// 2. Rezervna pretraga preko lpstat -e
	if len(printersMap) == 0 {
		cmdE := exec.Command("lpstat", "-e")
		if out, err := cmdE.Output(); err == nil {
			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				p := strings.TrimSpace(scanner.Text())
				if p != "" {
					printersMap[p] = true
				}
			}
		}
	}

	// 3. Pronalaženje podrazumevanog štampača
	cmdDef := exec.Command("lpstat", "-d")
	if outDef, err := cmdDef.Output(); err == nil {
		parts := strings.Split(string(outDef), ":")
		if len(parts) >= 2 {
			defaultPrinter = strings.TrimSpace(parts[1])
		}
	}

	printers := make([]string, 0, len(printersMap))
	for p := range printersMap {
		printers = append(printers, p)
	}

	return printers, defaultPrinter
}

// printOrSaveDialog otvara prozor za izbor štampača ili čuvanje u PDF
func printOrSaveDialog(doc document.Document) func() {
	return func() {
		if doc == nil {
			return
		}

		if idDoc, ok := doc.(*document.IdDocument); ok {
			if state.pdfCyrillicLabels {
				idDoc.SetCyrillicLabels()
			}
		}

		pdf, fileName, err := doc.BuildPdf()
		if err != nil {
			setStatus("error.generatingPdf", fmt.Errorf("generating PDF: %w", err))
			return
		}

		printers, defaultPrinter := getSystemPrinters()

		const saveAsPdfOption = "Sačuvaj kao PDF..."
		options := []string{saveAsPdfOption}
		options = append(options, printers...)

		selectedTarget := saveAsPdfOption
		if defaultPrinter != "" {
			selectedTarget = defaultPrinter
		} else if len(printers) > 0 {
			selectedTarget = printers[0]
		}

		selectWidget := widget.NewSelect(options, func(s string) {
			selectedTarget = s
		})
		selectWidget.SetSelected(selectedTarget)

		content := widget.NewForm(
			widget.NewFormItem("Odredište:", selectWidget),
		)

		d := dialog.NewCustomConfirm(
			"Štampanje dokumenta",
			"Potvrdi",
			"Odustani",
			content,
			func(confirmed bool) {
				if !confirmed {
					return
				}

				if selectedTarget == saveAsPdfOption {
					savePdfWithData(pdf, fileName)
					return
				}

				go func(targetPrinter string) {
					tempFile, err := os.CreateTemp("", "bas-celik-print-*.pdf")
					if err != nil {
						logger.Error(fmt.Errorf("creating temp file: %w", err))
						return
					}
					tempPath := tempFile.Name()

					if _, err := tempFile.Write(pdf); err != nil {
						tempFile.Close()
						_ = os.Remove(tempPath)
						logger.Error(fmt.Errorf("writing temp file: %w", err))
						return
					}
					tempFile.Close()

					cmd := exec.Command("lpr", "-P", targetPrinter, tempPath)
					cmdErr := cmd.Run()
					_ = os.Remove(tempPath)

					if cmdErr != nil {
						logger.Error(fmt.Errorf("lpr error: %w", cmdErr))
						setTimedStatus("Greška pri slanju na štampač")
						return
					}

					setTimedStatus("Poslato na: " + targetPrinter)
				}(selectedTarget)
			},
			state.window,
		)

		d.Resize(fyne.NewSize(380, 150))
		d.Show()
	}
}

// savePdfWithData otvara standardni dijalog za snimanje fajla
func savePdfWithData(pdf []byte, fileName string) {
	fDialog := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			setStatus("error.writingPdf", fmt.Errorf("writing PDF: %w", err))
			return
		}
		if w == nil {
			return
		}

		saveLastUsedDirectory(w.URI())

		if _, err = w.Write(pdf); err != nil {
			setStatus("error.writingPdf", fmt.Errorf("writing PDF: %w", err))
			return
		}

		if err = w.Close(); err != nil {
			setStatus("error.writingPdf", fmt.Errorf("writing PDF: %w", err))
			return
		}

		setStatus("ui.pdfSaved", nil)
	}, state.window)

	fDialog.SetFilter(storage.NewExtensionFileFilter([]string{".pdf"}))
	fDialog.SetFileName(fileName)
	fDialog.SetView(dialog.ListView)

	lastUsedDirectoryURI := getLastUsedDirectory()
	if lastUsedDirectoryURI != nil {
		fDialog.SetLocation(lastUsedDirectoryURI)
	}

	fDialog.Show()
}
