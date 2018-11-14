package sourcenet

import (
	"github.com/galaco/bitbuf"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/utils"
	"log"
)

type SplitPacket struct {
	netID int32
	sequenceNumber int32
	packetID int16
	splitSize int16
}

type DataFragment struct {
	Filename 	  		  string
	Buffer	 	  		  []byte
	SizeInBytes 		  uint32
	SizeInBits  		  uint32
	TransferID	  		  uint32	// Used only for files
	IsCompressed  		  bool		// Is bzip compressed
	SizeUncompressed 	  uint32
	AsTCP 		  		  bool		// Send as TCP stream
	NumFragments  		  int32
	AcknowledgedFragments int32		// Fragments sent and acknowledges
	PendingFragments	  int32		// Fragments sent, but not (yet) acknolwedged
	FragmentOffsets 	  []int32
}

type SubChannel struct {
	FirstFragment 		[maxStreams]int32
	NumFragments 		[maxStreams]int32
	SendSequenceCounter int32
	State 				int32	// 0=free, 1=scheduled to read, 2=send & waiting, 3=dirty
	Index 				int32	// index into containing channels subchannel array
}

func (channel *SubChannel) Free() {
	channel.State = subChannelFree
	channel.SendSequenceCounter = -1
	for i := 0; i < maxStreams; i++ {
		channel.NumFragments[i] = 0
		channel.FirstFragment[i] = -1
	}
}

// Channel is is responsible for processing received packets into
// appropriate formats.
type Channel struct {
	received	[maxStreams]DataFragment
	subChannels [maxSubChannels]SubChannel
	waiting		[maxStreams][]*DataFragment

	challengeValue int32
	challengeValueInStream bool

	inSequenceCounter 			   int32
	outSequenceAcknowledgedCounter int32

	inReliableState int32

	receivedProcessed []IMessage
}

// ProcessPacket Reads received packet header and determines if the
// packet is ready to be inspected outside on the netcode.
// Any packet not deemed ready (e.g. split packet) will be queued until it
// is ready
func (channel *Channel) ProcessPacket (message IMessage) bool {
	if message.Connectionless() == true {
		return true
	}

	recvdata := bitbuf.NewReader(message.Data())
	header,_ := recvdata.ReadUint32()

	if header == packetHeaderFlagSplit {

		if channel.HandleSplitPacket(recvdata) == 0 {
			return false
		}

		header,_ = recvdata.ReadUint32();
	}

	if header == packetHeaderFlagCompressed {
		log.Println("Unsupported compressed packet")
		return false
		//uncompressedSize := len(message.Data()) * 16;
		//
		//char*tmpbuffer = new char[uncompressedSize];
		//
		//
		//memmove(netrecbuffer, netrecbuffer + 4, msgsize + 4);
		//
		//NET_BufferToBufferDecompress(tmpbuffer, uncompressedSize, netrecbuffer, msgsize);
		//
		//memcpy(netrecbuffer, tmpbuffer, uncompressedSize);
		//
		//
		//recvdata.StartReading(netrecbuffer, uncompressedSize, 0);
		//printf("UNCOMPRESSED\n");
		//
		//
		//delete[] tmpbuffer;
		//tmpbuffer = 0;
	}

	recvdata.Seek(0)

	flags := channel.ReadHeader(message)
	if flags == -1 {
		return false
	}

	if flags & packetFlagReliable != 0 {
		shiftCount,_ := recvdata.ReadUnsignedBitInt32(3)
		bit := 1 << shiftCount

		for i := 0; i < maxStreams; i++ {
			if recvdata.ReadOneBit() != false {
				if channel.readSubChannelData(recvdata, i) == false {
					return false
				}
			}
		}

		channel.inReliableState = int32(utils.FlipBit(uint(channel.inReliableState), bit))

		for i := 0; i < maxStreams; i++ {
			if channel.checkReceivingList(i) == false {
				return false
			}
		}
	}


	// @TODO implement me
	//if channel.NeedsFragments() || flags & packetFlagTables != 0 {
	//	neededfragments = true
	//	NET_RequestFragments()
	//}

	return true
}

// HandleSplitPacket process a packet that contains multiple entries
// @TODO Implement me
func (channel *Channel) HandleSplitPacket(recvdata *bitbuf.Reader) int {
	//netrecbuffer = []SplitPacket from recvdata.Data()
	//header := netrecbuffer[0]
	//
	//// pHeader is network endian correct
	//sequenceNumber := header.sequenceNumber
	//packetID := header.packetID
	//// High byte is packet number
	//packetNumber := (packetID >> 8)
	//// Low byte is number of total packets
	//packetCount := (packetID & 0xff) - 1
	//
	//nSplitSizeMinusHeader := header.splitSize
	//
	//offset := (packetNumber * nSplitSizeMinusHeader);
	//
	//memcpy(splitpacket_compiled+ offset, netrecbuffer+ SPLIT_HEADER_SIZE, msgsize- SPLIT_HEADER_SIZE);
	//
	//
	//if packetNumber == packetCount {
	//	memset(netrecbuffer, 0, msgsize);
	//
	//
	//	splitsize = offset + msgsize;
	//	memcpy(netrecbuffer, splitpacket_compiled, splitsize);
	//	msgsize = splitsize;
	//	recvdata.StartReading(netrecbuffer, msgsize, 0);
	//
	//	return 1;
	//
	//}

	return 0
}

// ReadHeader parses the received packet header.
// Returned data is header flags value.
func (channel *Channel) ReadHeader(msg IMessage) int32 {
	message := bitbuf.NewReader(msg.Data())
	sequence,_ := message.ReadInt32()
	sequenceAcknowledged,_ := message.ReadInt32()
	flags,_ := message.ReadInt8()

	checksum := uint16(0)

	if !skipChecksum {
		checksum,_ = message.ReadUint16()

		offset := message.GetNumBitsRead() >> 3
		checkSumBytes := message.Data()[offset:len(message.Data())]
		dataCheckSum := utils.CRC16(checkSumBytes);

		if !skipChecksumValidation {
			if dataCheckSum != checksum {
				// checksum mismatch
				return -1
			}
		}
	}

	message.ReadInt8()
	//relState,_ := message.ReadInt8()

	numChoked := uint8(0)

	if flags & packetFlagChoked != 0 {
		numChoked,_ = message.ReadByte()
	}

	if flags & packetFlagChallenge != 0 {
		challenge,_ := message.ReadInt32()

		if channel.challengeValue == 0 {
			channel.challengeValue = challenge
		}

		if challenge != channel.challengeValue {
			// Bad challenge
			return -1
		}

		channel.challengeValueInStream = true
	} else if channel.challengeValueInStream == true {
		// stream contains challenge, but not provided?
		return -1
	}

	droppedPackets := sequence - (channel.inSequenceCounter + int32(numChoked) + 1)
	if droppedPackets > 0 {
		if droppedPackets > maxAllowedPacketDrop {
			return -1
		}
	}

	channel.inSequenceCounter = sequence
	channel.outSequenceAcknowledgedCounter = sequenceAcknowledged


	for i := 0; i < maxStreams; i++ {
		channel.checkWaitingList(i)
	}

	if sequence == 0x36 {
		flags |= packetFlagTables
	}

	return flags
}

func (channel *Channel) readSubChannelData(buf *bitbuf.Reader, stream int) bool {
	data := &channel.received[stream] // get list
	startFragment := int32(0)
	numFragments := int32(0)
	offset := uint(0)
	length := uint(0)

	singleBlock := buf.ReadOneBit() == false // is single block ?

	if singleBlock == false {
		startFragment,_ = buf.ReadUBitLong(maxFilesizeBits - fragmentBits) // 16 MB max
		numFragments,_ = buf.ReadUBitLong(3)  // 8 fragments per packet max
		offset = uint(startFragment * fragmentSize)
		length = uint(numFragments * fragmentSize)
	}

	if offset == 0 { // first fragment, read header info
		data.Filename = ""
		data.IsCompressed = false
		data.TransferID = 0

		if singleBlock {

			// data compressed ?
			if buf.ReadOneBit() == true {
				data.IsCompressed = true
				data.SizeUncompressed,_ = buf.ReadUBitLong(maxFilesizeBits)
			} else {
				data.IsCompressed = false
			}


			data.SizeInBytes,_ = buf.ReadInt32()

		} else {
			// is it a file ?
			if buf.ReadOneBit() == true {
				data.TransferID,_ = buf.ReadUBitLong(32)
				data.Filename,_ = buf.ReadString(maxOSPath)
			}

			// data compressed ?
			if buf.ReadOneBit() == true {
				data.IsCompressed = true;
				data.SizeUncompressed,_ = buf.ReadUBitLong(maxFilesizeBits)
			} else {
				data.IsCompressed = false;
			}

			data.SizeInBytes,_ = buf.ReadUBitLong(maxFilesizeBits);

		}

		if len(data.Buffer) > 0 {
			// last transmission was aborted, free data
			data.Buffer = make([]byte, 0)
		}

		data.SizeInBits = data.SizeInBytes * 8
		data.Buffer = make([]byte, utils.PadNumber(int32(data.SizeInBytes), 4))
		data.AsTCP = false
		data.NumFragments = int32((data.SizeInBytes + fragmentSize -1) / fragmentSize)
		data.AcknowledgedFragments = 0

		if singleBlock {
			numFragments = data.NumFragments
			length = uint(numFragments * fragmentSize)
		}
	} else {
		if data.Buffer == nil || len(data.Buffer) == 0 {
			// This can occur if the packet containing the "header" (offset == 0) is dropped.  Since we need the header to arrive we'll just wait
			//  for a retry
			return false;
		}
	}


	if (startFragment + numFragments) == data.NumFragments {
		// we are receiving the last fragment, adjust length
		rest := fragmentSize - (data.SizeInBytes % fragmentSize)
		if rest < 0xFF { //if (rest < FRAGMENT_SIZE)
			length -= uint(rest)
		}
	}

	data.Buffer[offset:],_ = buf.ReadBytes(length) // read data

	data.AcknowledgedFragments += numFragments

	return true
}

// checkReceivingList check if any data waiting on more
// fragments
func (channel *Channel) checkReceivingList(i int) bool {
	data := &channel.received[i]

	if data.Buffer == nil || len(data.Buffer) == 0 {
		return true
	}

	if data.AcknowledgedFragments < data.NumFragments {
		return true
	}

	if data.AcknowledgedFragments > data.NumFragments {
		//  Something went wrong. Received more fragments than expected
		return false
	}

	if data.IsCompressed == true {
		// decompress
		// data = decompressFragments(data)
	}


	if len(data.Filename) == 0 {
		channel.receivedProcessed = append(channel.receivedProcessed, message.NewGeneric(data.Buffer))
	} else {
		channel.receivedProcessed = append(channel.receivedProcessed, message.NewFile(data.Filename, data.Buffer))
	}

	// clean list
	if len(data.Buffer) > 0 {
		data.Buffer = make([]byte, 0)
		data.FragmentOffsets = make([]int32, 0)
		data.NumFragments = 0
	}

	return true
}


// checkWaitingList check if a packet waiting to send has
// been sent
func (channel *Channel) checkWaitingList(i int) {
	if channel.outSequenceAcknowledgedCounter == 0 || len(channel.waiting[i]) == 0 {
		return
	}

	data := channel.waiting[i][0]
	if data.AcknowledgedFragments == data.NumFragments {
		// All fragments sent
		// Remove from waiting list
		for j := 0; j < len(channel.waiting[i]); j++ {
			if channel.waiting[i][j] == data {
				channel.waiting[i] = append(channel.waiting[i][:j], channel.waiting[i][j+1:]...)
				break
			}
		}

		return
	} else if data.AcknowledgedFragments > data.NumFragments {
		// More fragments acknowledged than there are?
		return
	}
}

func (channel *Channel) GetReceivedAndProcessed() []IMessage {
	ret := channel.receivedProcessed
	channel.receivedProcessed = make([]IMessage, 0)
	return ret
}

func NewChannel() Channel {
	channel := Channel{
		receivedProcessed: make([]IMessage, 0),
	}

	for i := 0; i < maxSubChannels; i++ {
		channel.subChannels[i].Index  = int32(i)
		channel.subChannels[i].Free()
	}


	return channel
}