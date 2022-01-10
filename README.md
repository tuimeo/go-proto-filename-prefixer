# go-proto-filename-prefixer

Add prefix to `name` field of descriptor of **generated protobuf go** files, which allows user to avoid name conflict problem without changing the exisiting proto build struct.

Proto filename conflict problem: https://github.com/golang/protobuf/issues/1122

## How it works

1. Scan the specified directory for any '*.pb.go' files whichs looks like a generated proto file.
2. Parse the `raw desc` part of the file, convert the hex back to [FileDescriptorProto](https://github.com/protocolbuffers/protobuf/blob/master/src/google/protobuf/descriptor.proto)
3. Prepend the specified string to `name` field.
4. Marshal to hex string and replace back to the `pb.go` file, also update header information.

## Usage