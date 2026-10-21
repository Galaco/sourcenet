package netchan

import (
	"math"
	"time"
)

// Flow selects a traffic direction for statistics.
type Flow int

// Traffic directions.
const (
	FlowOutgoing Flow = 0
	FlowIncoming Flow = 1
)

const (
	flowOutgoing  = FlowOutgoing
	flowIncoming  = FlowIncoming
	flowAvg       = 3.0 / 4.0
	flowInterval  = 0.25 // seconds between statistic recomputations
	framesBackup  = 64
	framesMask    = framesBackup - 1
	latencyUnseen = -1.0
)

type netFrame struct {
	time       float64
	size       int
	latency    float64
	avgLatency float64
	valid      bool
	choked     int
	dropped    int
}

type flow struct {
	nextCompute      float64
	avgBytesPerSec   float64
	avgPacketsPerSec float64
	avgLoss          float64
	avgChoke         float64
	avgLatency       float64
	latency          float64
	totalPackets     int
	totalBytes       int
	currentIndex     int32
	frames           [framesBackup]netFrame
}

// FlowStats summarises one direction of a channel (net_graph data).
type FlowStats struct {
	Latency          time.Duration // most recent ping estimate
	AvgLatency       time.Duration
	AvgLoss          float64 // fraction of packets lost [0, 1]
	AvgChoke         float64 // fraction of packets choked [0, 1]
	AvgBytesPerSec   float64
	AvgPacketsPerSec float64
	TotalPackets     int
	TotalBytes       int
}

// Stats returns traffic statistics for a direction.
func (c *Channel) Stats(f Flow) FlowStats {
	fl := &c.flow[f]
	return FlowStats{
		Latency:          seconds(fl.latency),
		AvgLatency:       seconds(fl.avgLatency),
		AvgLoss:          fl.avgLoss,
		AvgChoke:         fl.avgChoke,
		AvgBytesPerSec:   fl.avgBytesPerSec,
		AvgPacketsPerSec: fl.avgPacketsPerSec,
		TotalPackets:     fl.totalPackets,
		TotalBytes:       fl.totalBytes,
	}
}

func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func (c *Channel) netTime(now time.Time) float64 {
	return now.Sub(c.connectTime).Seconds()
}

// flowNewPacket records a sent or received packet and resolves the latency
// of the packet it acknowledges (CNetChan::FlowNewPacket).
func (c *Channel) flowNewPacket(f Flow, now time.Time, seq, ack int32, choked, dropped, size int) {
	t := c.netTime(now)
	fl := &c.flow[f]
	if seq > fl.currentIndex {
		var frame *netFrame
		for i, n := fl.currentIndex+1, 0; i <= seq && n < framesBackup; i, n = i+1, n+1 {
			back := int(seq - i)
			frame = &fl.frames[int(i)&framesMask]
			*frame = netFrame{
				time:       t,
				latency:    latencyUnseen,
				avgLatency: c.flow[flowOutgoing].avgLatency,
			}
			if back < choked+dropped {
				if back < choked {
					frame.choked = 1
				} else {
					frame.dropped = 1
				}
			}
		}
		frame.dropped = dropped
		frame.choked = choked
		frame.size = size
		frame.valid = true
	}
	fl.totalPackets++
	fl.currentIndex = seq

	other := &c.flow[1-f]
	if ack <= other.currentIndex-framesBackup {
		return
	}
	af := &other.frames[int(ack)&framesMask]
	if af.valid && af.latency == latencyUnseen {
		af.latency = math.Max(t-af.time, 0)
	}
}

// flowUpdate accumulates bytes and periodically recomputes averages
// (CNetChan::FlowUpdate).
func (c *Channel) flowUpdate(f Flow, now time.Time, bytes int) {
	t := c.netTime(now)
	fl := &c.flow[f]
	fl.totalBytes += bytes
	if fl.nextCompute > t {
		return
	}
	fl.nextCompute = t + flowInterval

	var valid, invalid, totalBytes, latencyCount, choked int
	var totalLatency float64
	start, end := math.MaxFloat64, 0.0
	for i := range fl.frames {
		fr := &fl.frames[i]
		if !fr.valid {
			invalid++
			continue
		}
		start = math.Min(start, fr.time)
		end = math.Max(end, fr.time)
		valid++
		choked += fr.choked
		totalBytes += fr.size
		if fr.latency > latencyUnseen {
			totalLatency += fr.latency
			latencyCount++
		}
	}
	if span := end - start; span > 0 {
		fl.avgBytesPerSec = fl.avgBytesPerSec*flowAvg + (1-flowAvg)*float64(totalBytes)/span
		fl.avgPacketsPerSec = fl.avgPacketsPerSec*flowAvg + (1-flowAvg)*float64(valid)/span
	}
	if total := valid + invalid; total > 0 {
		fl.avgLoss = math.Max(fl.avgLoss*flowAvg+(1-flowAvg)*float64(invalid-choked)/float64(total), 0)
		fl.avgChoke = fl.avgChoke*flowAvg + (1-flowAvg)*float64(choked)/float64(total)
	}
	if latencyCount > 0 {
		ping := totalLatency / float64(latencyCount)
		fl.latency = ping
		fl.avgLatency = fl.avgLatency*flowAvg + (1-flowAvg)*ping
	}
}
