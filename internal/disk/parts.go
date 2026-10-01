package disk

import gdisk "github.com/shirou/gopsutil/v4/disk"

func partitions() ([]gdisk.PartitionStat, error) { return gdisk.Partitions(true) }
