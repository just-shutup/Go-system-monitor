package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/yusufpapurcu/wmi"
)

type SystemStats struct {
	CPU  CPUStats  `json:"cpu"`
	RAM  RAMStats  `json:"ram"`
	GPU  GPUStats  `json:"gpu"`
	Disk DiskStats `json:"disk"`
}

type CPUStats struct {
	Total float64   `json:"total"`
	Cores []float64 `json:"cores"`
}

type RAMStats struct {
	UsedGB  float64 `json:"usedGb"`
	TotalGB float64 `json:"totalGb"`
	Percent float64 `json:"percent"`
}

type GPUStats struct {
	Name string  `json:"name"`
	Load float64 `json:"load"`
	Temp float64 `json:"temp"`
}

type DiskStats struct {
	ReadIOPS  float64 `json:"readIops"`
	WriteIOPS float64 `json:"writeIops"`
}

type Win32_VideoController struct {
	Name string
}

type Win32_GPUEngine struct {
	UtilizationPercentage uint64
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var (
	prevDiskCounters map[string]disk.IOCountersStat
	diskMutex        sync.Mutex
)

func getStats() SystemStats {
	stats := SystemStats{}

	totalPerc, _ := cpu.Percent(0, false)
	corePerc, _ := cpu.Percent(0, true)
	stats.CPU = CPUStats{
		Total: totalPerc[0],
		Cores: corePerc,
	}

	v, _ := mem.VirtualMemory()
	stats.RAM = RAMStats{
		UsedGB:  float64(v.Used) / 1024 / 1024 / 1024,
		TotalGB: float64(v.Total) / 1024 / 1024 / 1024,
		Percent: v.UsedPercent,
	}

	stats.GPU = getGPUStats()
	stats.Disk = getDiskIOPS()

	return stats
}

func getGPUStats() GPUStats {
	gpu := GPUStats{Name: "Unknown GPU", Load: 0, Temp: 0}

	var dst []Win32_VideoController
	err := wmi.Query("SELECT Name FROM Win32_VideoController", &dst)
	if err == nil && len(dst) > 0 {
		gpu.Name = dst[0].Name
	}

	var gpuEngines []Win32_GPUEngine
	err = wmi.Query("SELECT UtilizationPercentage FROM Win32_PerfFormattedData_GPUPerformanceCounters_GPUEngine", &gpuEngines)
	if err == nil && len(gpuEngines) > 0 {
		var maxLoad uint64
		for _, eng := range gpuEngines {
			if eng.UtilizationPercentage > maxLoad {
				maxLoad = eng.UtilizationPercentage
			}
		}
		gpu.Load = float64(maxLoad)
	}

	return gpu
}

func getDiskIOPS() DiskStats {
	diskMutex.Lock()
	defer diskMutex.Unlock()

	currentCounters, err := disk.IOCounters("C:")
	if err != nil {
		return DiskStats{}
	}

	if prevDiskCounters == nil {
		prevDiskCounters = currentCounters
		return DiskStats{}
	}

	var readIOPS, writeIOPS float64
	for k, cur := range currentCounters {
		if prev, ok := prevDiskCounters[k]; ok {
			readIOPS = float64(cur.ReadCount - prev.ReadCount)
			writeIOPS = float64(cur.WriteCount - prev.WriteCount)
		}
	}

	prevDiskCounters = currentCounters

	return DiskStats{
		ReadIOPS:  readIOPS,
		WriteIOPS: writeIOPS,
	}
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()

	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		stats := getStats()
		data, err := json.Marshal(stats)
		if err != nil {
			log.Println("JSON marshal error:", err)
			continue
		}

		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Println("WS write error:", err)
			break
		}
	}
}

func main() {
	http.HandleFunc("/ws", handleWS)
	// фронт должен быть в statics
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	fmt.Println("Server starting on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
