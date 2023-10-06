package utilities

import (
	"fmt"
	"os/exec"
	"strings"
)

type CPUDetails struct {
	Vendor    string
	ModelName string
	Cores     string
	MHZ       string
	Cache     string
}

func Hwinfo() {
	// Run the hwinfo command and capture its output
	cmd := exec.Command("hwinfo")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error running lspci:", err)
		return
	}

	// Split the output into lines
	lines := strings.Split(string(output), "\n")

	var cpuDetails []CPUDetails
	var currentCPUDetails CPUDetails
	var sliceOfCpuBlocks []string

	withinCpuBlock := false
	cpuAlreadyProcessed := false
	for _, line := range lines {
		// Detect the start of a new CPU section
		if strings.HasPrefix(line, ">> cpu") {
			for _, v := range sliceOfCpuBlocks {
				if v == line {
					cpuAlreadyProcessed = true
					break
				}
			}
			if cpuAlreadyProcessed {
				continue
			}
			currentCPUDetails = CPUDetails{}
			withinCpuBlock = true
			// hwinfo issue multiple blocks/sections for each CPU
			// slice used to ensure CPU block has not already been proceessed and to avoid duplicates
			sliceOfCpuBlocks = append(sliceOfCpuBlocks, line)
		} else if withinCpuBlock && strings.Contains(line, "model name") {
			modelName := strings.Split(line, ":")
			currentCPUDetails.ModelName = modelName[1]
			if strings.Contains(line, "AMD") {
				currentCPUDetails.Vendor = "AMD"
			} else if strings.Contains(line, "Intel") {
				currentCPUDetails.Vendor = "Intel"
			}
		} else if withinCpuBlock && strings.Contains(line, "cpu cores") {
			cpuCores := strings.Split(line, ":")
			currentCPUDetails.Cores = cpuCores[1]
		} else if withinCpuBlock && strings.Contains(line, "cpu MHz") {
			cpuMHz := strings.Split(line, ":")
			currentCPUDetails.MHZ = cpuMHz[1]
		} else if withinCpuBlock && strings.Contains(line, "cache size") {
			cpuCache := strings.Split(line, ":")
			currentCPUDetails.Cache = cpuCache[1]
		} else if withinCpuBlock && line == "" {
			withinCpuBlock = false
			cpuDetails = append(cpuDetails, currentCPUDetails)
		}
	}

	fmt.Println("########CPU DETAILS########")
	fmt.Println("CPU Details - Vendor: ", cpuDetails[0].Vendor)
	fmt.Println("CPU Details - ModelName: ", cpuDetails[0].ModelName)
	fmt.Println("CPU Details - Cores: ", cpuDetails[0].Cores)
	fmt.Println("CPU Details - MHZ: ", cpuDetails[0].MHZ)
	fmt.Println("CPU Details - Cache: ", cpuDetails[0].Cache)

}
