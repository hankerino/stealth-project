module github.com/hankerino/stealth-project/services/order

go 1.22

require (
	github.com/hankerino/stealth-project/libs/proto/gen v0.0.0
	github.com/lib/pq v1.10.9
	github.com/segmentio/kafka-go v0.4.47
	google.golang.org/grpc v1.64.1
)

require (
	github.com/klauspost/compress v1.15.9 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240318140521-94a12d6c2237 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

replace github.com/hankerino/stealth-project/libs/proto/gen => ../../libs/proto/gen
