package upscaler

import (
	"encoding/binary"

	"golang.org/x/sys/windows/registry"
)

// displayClass is where Windows keeps each display adapter's driver
// settings, including the memory it reports to Task Manager.
const displayClass = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

func systemGPUMemory() []gpuMem {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	subs, _ := k.ReadSubKeyNames(-1)
	var mems []gpuMem
	for _, s := range subs {
		a, err := registry.OpenKey(k, s, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		name, _, _ := a.GetStringValue("DriverDesc")
		size := adapterMemory(a)
		a.Close()
		if name != "" && size > 0 {
			mems = append(mems, gpuMem{name, int(size >> 20)})
		}
	}
	return mems
}

// adapterMemory reads the 64-bit size, falling back on the 32-bit one that
// stops at 4 GB; drivers store either as a number or as raw bytes.
func adapterMemory(k registry.Key) uint64 {
	for _, v := range []string{"HardwareInformation.qwMemorySize", "HardwareInformation.MemorySize"} {
		if n, _, err := k.GetIntegerValue(v); err == nil && n > 0 {
			return n
		}
		if b, _, err := k.GetBinaryValue(v); err == nil {
			switch len(b) {
			case 8:
				return binary.LittleEndian.Uint64(b)
			case 4:
				return uint64(binary.LittleEndian.Uint32(b))
			}
		}
	}
	return 0
}
