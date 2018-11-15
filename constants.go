package sourcenet

const subChannelFree = 0
const packetHeaderFlagQuery = 0xffffffff
const packetHeaderFlagSplit = 0xffffffff - 1
const packetHeaderFlagCompressed = 0xffffffff - 2

const checksumPackets = true

const packetFlagReliable = 1 << 0
const packetFlagCompressed = 1 << 1
const packetFlagEncrypted = 1 << 2
const packetFlagSplit = 1 << 3
const packetFlagChoked = 1 << 4
const packetFlagChallenge = 1 << 5
const packetFlagTables = int32(1 << 10)

const netmsgTypeBits = 6

const maxFileSizeBits = 26
const maxFileSize = (1 << maxFileSizeBits) - 1
const fragmentBits = uint32(8)
const fragmentSize = 1 << fragmentBits

// Stream 0 = regular, 1 = file stream
const maxStreams = 2
const maxSubChannels = 8
const maxOSPath = 260

const maxAllowedPacketDrop = 0
const minRoutablePayload = 16

const skipChecksum = false
const skipChecksumValidation = true
