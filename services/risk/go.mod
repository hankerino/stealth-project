module github.com/hankerino/stealth-project/services/risk

go 1.22

require (
	github.com/hankerino/stealth-project/libs/proto/gen v0.0.0
	github.com/lib/pq v1.10.9
	github.com/segmentio/kafka-go v0.4.47
	google.golang.org/grpc v1.64.1
	google.golang.org/protobuf v1.34.2
)

replace github.com/hankerino/stealth-project/libs/proto/gen => ../../libs/proto/gen
