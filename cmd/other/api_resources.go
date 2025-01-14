// apiResources.go

package other

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cloudforet-io/cfctl/pkg/configs"
	"github.com/cloudforet-io/cfctl/pkg/format"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"

	"github.com/pterm/pterm"
)

var endpoints string

func loadEndpointsFromCache(currentEnv string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to find home directory: %v", err)
	}

	// Read from environment-specific cache file
	cacheFile := filepath.Join(home, ".cfctl", "cache", currentEnv, "endpoints.yaml")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	var endpoints map[string]string
	if err := yaml.Unmarshal(data, &endpoints); err != nil {
		return nil, err
	}

	return endpoints, nil
}

var ApiResourcesCmd = &cobra.Command{
	Use:   "api_resources",
	Short: "Displays supported API resources",
	Example: `  # List all API resources for all services
  $ cfctl api_resources

  # List API resources for a specific service
  $ cfctl api_resources -s identity

  # List API resources for multiple services
  $ cfctl api_resources -s identity,inventory,repository`,
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Unable to find home directory: %v", err)
		}

		settingPath := filepath.Join(home, ".cfctl", "setting.yaml")

		// Read main setting file
		mainV := viper.New()
		mainV.SetConfigFile(settingPath)
		mainV.SetConfigType("yaml")
		mainConfigErr := mainV.ReadInConfig()

		var currentEnv string
		var envConfig map[string]interface{}

		if mainConfigErr == nil {
			currentEnv = mainV.GetString("environment")
			if currentEnv != "" {
				envConfig = mainV.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))
			}
		}

		if envConfig == nil {
			return
		}

		// Try to load endpoints from cache first
		endpointsMap, err := loadEndpointsFromCache(currentEnv)
		if err != nil {
			// If cache loading fails, fall back to fetching from identity service
			endpointName, ok := envConfig["endpoint"].(string)
			if !ok || endpointName == "" {
				return
			}

			endpointsMap, err = configs.FetchEndpointsMap(endpointName)
			if err != nil {
				log.Fatalf("Failed to fetch endpointsMap from '%s': %v", endpointName, err)
			}
		}

		// Load short names configuration
		shortNamesFile := filepath.Join(home, ".cfctl", "short_names.yaml")
		shortNamesMap := make(map[string]string)
		if _, err := os.Stat(shortNamesFile); err == nil {
			file, err := os.Open(shortNamesFile)
			if err != nil {
				log.Fatalf("Failed to open short_names.yaml file: %v", err)
			}
			defer file.Close()

			err = yaml.NewDecoder(file).Decode(&shortNamesMap)
			if err != nil {
				log.Fatalf("Failed to decode short_names.yaml: %v", err)
			}
		}

		// Process endpoints provided via flag
		if endpoints != "" {
			selectedEndpoints := strings.Split(endpoints, ",")
			for i := range selectedEndpoints {
				selectedEndpoints[i] = strings.TrimSpace(selectedEndpoints[i])
			}
			var allData [][]string

			for _, endpointName := range selectedEndpoints {
				serviceEndpoint, ok := endpointsMap[endpointName]
				if !ok {
					log.Printf("No endpoint found for %s", endpointName)
					continue
				}

				result, err := format.FetchServiceResources(endpointName, serviceEndpoint, shortNamesMap)
				if err != nil {
					log.Printf("Error processing service %s: %v", endpointName, err)
					continue
				}

				allData = append(allData, result...)
			}

			sort.Slice(allData, func(i, j int) bool {
				return allData[i][0] < allData[j][0]
			})

			renderTable(allData)
			return
		}

		// If no specific endpoints are provided, list all services
		var wg sync.WaitGroup
		dataChan := make(chan [][]string, len(endpointsMap))
		errorChan := make(chan error, len(endpointsMap))

		for service, endpoint := range endpointsMap {
			wg.Add(1)
			go func(service, endpoint string) {
				defer wg.Done()
				result, err := format.FetchServiceResources(service, endpoint, shortNamesMap)
				if err != nil {
					errorChan <- fmt.Errorf("Error processing service %s: %v", service, err)
					return
				}
				dataChan <- result
			}(service, endpoint)
		}

		wg.Wait()
		close(dataChan)
		close(errorChan)

		if len(errorChan) > 0 {
			for err := range errorChan {
				log.Println(err)
			}
		}

		var allData [][]string
		for data := range dataChan {
			allData = append(allData, data...)
		}

		sort.Slice(allData, func(i, j int) bool {
			return allData[i][0] < allData[j][0]
		})

		renderTable(allData)
	},
}

func renderTable(data [][]string) {
	// Calculate the dynamic width for the "Verb" column
	terminalWidth := pterm.GetTerminalWidth()
	usedWidth := 30 + 20 + 15 // Estimated widths for Service, Resource, and Alias columns
	verbColumnWidth := terminalWidth - usedWidth
	if verbColumnWidth < 20 {
		verbColumnWidth = 20 // Minimum width for Verb column
	}

	// Use two distinct colors for alternating services
	alternateColors := []pterm.Color{
		pterm.FgLightBlue, pterm.FgLightYellow, pterm.FgLightMagenta,
		pterm.FgGreen, pterm.FgLightRed, pterm.FgBlue, pterm.FgLightGreen,
	}

	currentColorIndex := 0
	previousService := ""

	table := pterm.TableData{{"Service", "Verb", "Resource", "Alias"}}

	for _, row := range data {
		service := row[0]

		if service != previousService {
			currentColorIndex = (currentColorIndex + 1) % len(alternateColors)
			previousService = service
		}

		color := alternateColors[currentColorIndex]
		coloredStyle := pterm.NewStyle(color)

		serviceColored := coloredStyle.Sprint(row[0])
		resourceColored := coloredStyle.Sprint(row[2])
		aliasColored := coloredStyle.Sprint(row[3])

		// Split verbs into multiple lines if needed
		verbs := splitIntoLinesWithComma(row[1], verbColumnWidth)
		for i, line := range verbs {
			if i == 0 {
				table = append(table, []string{
					serviceColored,
					coloredStyle.Sprint(line),
					resourceColored,
					aliasColored,
				})
			} else {
				table = append(table, []string{
					"",
					coloredStyle.Sprint(line),
					"",
					"",
				})
			}
		}
	}

	pterm.DefaultTable.WithHasHeader().WithData(table).Render()
}

func splitIntoLinesWithComma(text string, maxWidth int) []string {
	words := strings.Split(text, ", ")
	var lines []string
	var currentLine string

	for _, word := range words {
		if len(currentLine)+len(word)+2 > maxWidth { // +2 accounts for the ", " separator
			lines = append(lines, currentLine+",")
			currentLine = word
		} else {
			if currentLine != "" {
				currentLine += ", "
			}
			currentLine += word
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

func init() {
	ApiResourcesCmd.Flags().StringVarP(&endpoints, "service", "s", "", "Specify the services to connect to, separated by commas (e.g., 'identity', 'identity,inventory')")
}
