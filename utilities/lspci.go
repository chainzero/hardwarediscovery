package utilities

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type GPUDetails struct {
	GPUModel     string
	Subsystem    string
	PhysicalSlot string
	Capabilities string
	KernelDriver string
}

func Lspci() {
	// Run the lspci command and capture its output
	cmd := exec.Command("lspci", "-v")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error running lspci:", err)
		return
	}

	// Split the output into lines
	lines := strings.Split(string(output), "\n")

	var gpuDetails []GPUDetails
	var currentGPUDetails GPUDetails

	withinGpuBlock := false
	var gpuName string
	for _, line := range lines {
		// Detect the start of a new GPU section

		if strings.Contains(line, "controller: NVIDIA") {
			currentGPUDetails = GPUDetails{}
			withinGpuBlock = true
			// Define a regular expression pattern to match the GPU name
			pattern := `\[(.*?)\]`
			re := regexp.MustCompile(pattern)
			match := re.FindStringSubmatch(line)
			// Check if a match was found
			if len(match) > 1 {
				gpuName = strings.TrimSpace(match[1]) // Remove leading and trailing whitespace
			} else {
				fmt.Println("GPU name not found in the string.")
			}
			currentGPUDetails.GPUModel = gpuName
		} else if withinGpuBlock && strings.Contains(line, "Subsystem") {
			currentGPUDetails.Subsystem = line
		} else if withinGpuBlock && strings.Contains(line, "Physical Slot") {
			currentGPUDetails.PhysicalSlot = line
		} else if withinGpuBlock && strings.Contains(line, "Capabilities") {
			currentGPUDetails.Capabilities = line
		} else if withinGpuBlock && strings.Contains(line, "Kernel driver") {
			currentGPUDetails.KernelDriver = line
		} else if withinGpuBlock && line == "" {
			withinGpuBlock = false
			gpuDetails = append(gpuDetails, currentGPUDetails)
		}
	}

	fmt.Println("########GPU DETAILS########")
	fmt.Println("GPU Details - Model: ", gpuDetails[0].GPUModel)
	fmt.Println("GPU Details - Quantity: ", len(gpuDetails))

}
