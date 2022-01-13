# go-proto-filename-prefixer

Proto filename conflict problem: https://github.com/golang/protobuf/issues/1122

This is a small tool to quick fix this problem by updating the embedded filename inside the **generated protobuf go** files.

## How it works

1. Scan the specified directory for any '*.pb.go' files whichs looks like a generated proto file.
2. Parse the `raw desc` part of the file, convert the hex back to [FileDescriptorProto](https://github.com/protocolbuffers/protobuf/blob/master/src/google/protobuf/descriptor.proto)
3. Prepend the specified string to `name` field.
4. Marshal to hex string and replace back to the `pb.go` file, also update header information.

## Usage

1. Install binary

    `go install github.com/tuimeo/go-proto-filename-prefixer@latest`

2. Add script in your build process, right after the generation of go proto file. (Usualy after `protoc` / `buf` command line)

    `go-proto-filename-prefixer <directory where pb.go files resident> <prefix to add>`


## Example

You have project struct as followed:

```
*project root <-- proto include path
   api/    <-- proto files, also generated pb.go files
```

Your generated file will have embeded filename like **api/common.proto**.

Run this command under project root:

```
go-proto-filename-prefixer api foo/bar/
```

Then the embeded filename will become **foo/bar/api/common.proto**, which will help to avoid conflic.

To get verbose output, run with `-v` at the end like this:

```
go-proto-filename-prefixer api foo/bar/ -v
```