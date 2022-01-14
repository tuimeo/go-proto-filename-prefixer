package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fatih/color"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

var errNotATargetPbGo = errors.New("not a target pb.go file, just ignore")
var verboseMode = false

//Metadata: "api/wechatwork_internal.proto",
var grpcMetadataLineMatcher = regexp.MustCompile(`Metadata:\s+"(\S+)",`)

func printUsageExit(msg string) {
	if msg != "" {
		fmt.Fprintln(os.Stderr, "Error: "+msg)
	}
	fmt.Fprintf(os.Stderr, "Usage: %v <pb.go directory> <prefix to add>\n", filepath.Base(os.Args[0]))
	os.Exit(-1)
}

func removeFilleByIndex(s *[]string, index int) {
	*s = append((*s)[:index], (*s)[index+1:]...)
}

func firstDir(name string) string {
	protoDir := filepath.Dir(name)
	dirs := strings.Split(protoDir, string(filepath.Separator))
	return dirs[0]
}

func outputResult(filename string, from string, to string, err error) {
	if err == nil {
		if verboseMode {
			color.Green("%v: %v ==> %v", filename, from, to)
		}
	} else {
		if err == errNotATargetPbGo {
			if verboseMode {
				// just ignore this file, it'okay
				color.Yellow("%v: %v", filename, err.Error())
			}
		} else {
			fmt.Fprintln(os.Stderr, color.RedString("%v: %v", filename, err.Error())) // always output if error to stderr
		}
	}
}

func patchGrpcFile(pbFile string, from string, to string) {
	// pbFile: xxxxx.pb.go, find xxxxx_grpc.pb.go
	name := strings.TrimSuffix(pbFile, ".pb.go")
	grpcFileName := fmt.Sprintf("%v_grpc.pb.go", name)

	fileContent, err := os.ReadFile(grpcFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return
		} else {
			outputResult(grpcFileName, "", "", err)
			return
		}
	}
	lines := strings.Split(string(fileContent), "\n")
	metadataLineNum := 0
	metadataVal := ""
	for idx, l := range lines {
		if m := grpcMetadataLineMatcher.FindStringSubmatch(l); m != nil {
			metadataLineNum = idx
			metadataVal = m[1]
			break
		}
	}

	if metadataLineNum == 0 {
		// seems not a valid grpc file
		outputResult(grpcFileName, "", "", fmt.Errorf("unrecognized _grpc.pb.go format"))
		return
	} else {
		if metadataVal != from {
			outputResult(grpcFileName, "", "", fmt.Errorf("unexpected metadata value"))
			return
		} else {
			f, err := os.Create(grpcFileName)
			if err != nil {
				outputResult(grpcFileName, "", "", err)
			}
			for _, l := range lines[0:metadataLineNum] {
				f.WriteString(l + "\n")
			}

			f.WriteString(fmt.Sprintf("\tMetadata: \"%v\",\n", to))

			for idx, l := range lines[metadataLineNum+1:] {
				if idx != len(lines)-metadataLineNum-2 {
					f.WriteString(l + "\n")
				} else {
					f.WriteString(l)
				}
			}
		}
	}
}

func main() {
	// open directory
	if len(os.Args) < 3 {
		printUsageExit("Please specify target directory and prefix")
	}

	if len(os.Args) >= 4 && os.Args[3] == "-v" {
		verboseMode = true
	}

	// scan for .pb.go files
	pbFiles := []string{}
	filepath.WalkDir(os.Args[1], func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Fail to access %v: %v", path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}

		if strings.HasSuffix(path, ".pb.go") {
			pbFiles = append(pbFiles, path)
		}

		return nil
	})

	// open file, check if contains `var XXXX protoreflect.FileDescriptor`. If yes, consider as valid proto file
	sourceHeaderLineMatcher := regexp.MustCompile(`^//\s+source:\s+(.+\.proto)$`)
	fileDescLineMatcher := regexp.MustCompile(`^var\s+(\w+)\s+protoreflect.FileDescriptor$`)
	var fileDescDataStartMatcher *regexp.Regexp
	var fileCursor = 0
	prefixRecord := make(map[string]string)
	pendingForDep := make(map[string]string)
	lastFileCount := len(pbFiles)

filescan:
	for len(pbFiles) > 0 {

		if fileCursor >= len(pbFiles) {
			if lastFileCount == len(pbFiles) {
				fmt.Fprintln(os.Stderr, color.RedString("Unable to process following files due to missing dependency, which also should be prefixed:"))
				for f, d := range pendingForDep {
					fmt.Fprintln(os.Stderr, color.RedString("\t* %v (depends on '%v')", f, d))
				}
				os.Exit(-1)
			}
			fileCursor = 0 // loop from the beginning
			pendingForDep = make(map[string]string)
			lastFileCount = len(pbFiles)
		}
		pbf := pbFiles[fileCursor]

		fileContent, err := os.ReadFile(pbf)
		if err != nil {
			outputResult(pbf, "", "", err)

			// can't read current file, have to ignore
			removeFilleByIndex(&pbFiles, fileCursor)
			continue
		}

		lines := strings.Split(string(fileContent), "\n")
		var originalProtoFile, fileDescVar string
		var originalProtoLine, dataBeginLine, dataEndLine int
		var pendingForData = false
		var fileDescPbRaw = new(bytes.Buffer)

		for idx, l := range lines {
			if originalProtoFile == "" {
				if m := sourceHeaderLineMatcher.FindStringSubmatch(l); m != nil {
					originalProtoFile = m[1]
					originalProtoLine = idx

					if strings.Contains(lines[idx+1], "prefixed by go-proto-filename-prefixer") {
						outputResult(pbf, "", "", fmt.Errorf("alreay prefixed"))
						removeFilleByIndex(&pbFiles, fileCursor)
						continue filescan
					}
					continue
				}
			}

			if fileDescVar == "" {
				if m := fileDescLineMatcher.FindStringSubmatch(l); m != nil {
					fileDescVar = m[1]
					fileDescDataStartMatcher = regexp.MustCompile(fmt.Sprintf(`var\s+%v_rawDesc\s+=\s+\[\]byte\{`, strings.ToLower(fileDescVar)))
					continue
				}
			}

			if !pendingForData && fileDescDataStartMatcher != nil && fileDescDataStartMatcher.MatchString(l) {
				pendingForData = true
				continue
			}

			if pendingForData {
				// if encounter }, means end
				trimedLine := strings.TrimSpace(l)
				if trimedLine == "" {
					continue
				}

				if dataBeginLine > 0 && trimedLine == "}" {
					dataEndLine = idx
					pendingForData = false
					break
				}

				// must like this -- 0x0a, 0x15, 0x61, 0x70, 0x69, 0x2f, 0x61, 0x70, 0x70, 0x6d, 0x67, 0x72, 0x5f, 0x75, 0x73, 0x65,
				hexs := strings.Split(trimedLine, ",")
				if dataBeginLine == 0 {
					dataBeginLine = idx
				}

				// convert from hex to raw bytes
				for _, h := range hexs {
					h = strings.TrimSpace(h)
					if len(h) == 0 {
						continue
					}
					d, err := hex.DecodeString(h[2:])
					if err != nil {
						outputResult(pbf, "", "", fmt.Errorf("invalid hex data -- %v, %v", h[2:], err))
						removeFilleByIndex(&pbFiles, fileCursor)
						continue filescan
					}
					fileDescPbRaw.Write(d)
				}
			}
		}

		if pendingForData {
			outputResult(pbf, "", "", fmt.Errorf("uncompleted data"))
			removeFilleByIndex(&pbFiles, fileCursor)
			continue
		}

		if dataEndLine == 0 || dataBeginLine == 0 {
			outputResult(pbf, "", "", errNotATargetPbGo)
			removeFilleByIndex(&pbFiles, fileCursor)
			continue
		}

		// unmarshal as `descriptorpb.FileDescriptorProto`
		fdp := &descriptorpb.FileDescriptorProto{}
		err = proto.Unmarshal(fileDescPbRaw.Bytes(), fdp)
		if err != nil {
			outputResult(pbf, "", "", err)
			removeFilleByIndex(&pbFiles, fileCursor)
			continue
		}

		// get the first "dir" of this file, protos are in root, it will be "."
		fd := firstDir(*fdp.Name)

		// check if there is any dependency under the same "dir"
		for idx, pd := range fdp.Dependency {
			dfd := firstDir(pd)

			if dfd == fd {
				// if yes, and check if these dependecy had been processed
				// we can't directly prefix the dependency proto under same dir unless it really exists
				if depPrefixedTo, ok := prefixRecord[pd]; !ok {
					// if not processed, just ignore this file this time
					if verboseMode {
						color.Yellow("%v: Dependency '%v' unprocessed, work later", pbf, pd)
					}
					fileCursor++
					pendingForDep[pbf] = pd
					continue filescan
				} else {
					fdp.Dependency[idx] = depPrefixedTo
				}
			}
		}

		// prepend prefix
		fromFilename := *fdp.Name
		toFilename := os.Args[2] + fromFilename
		fdp.Name = &toFilename

		b, err := proto.Marshal(fdp)
		if err != nil {
			outputResult(pbf, "", "", err)
			removeFilleByIndex(&pbFiles, fileCursor)
			continue
		}

		// write back and add header info
		f, err := os.Create(pbf)
		if err != nil {
			outputResult(pbf, "", "", err)
			removeFilleByIndex(&pbFiles, fileCursor)
			continue
		}

		for _, hl := range lines[0 : originalProtoLine+1] {
			f.WriteString(hl + "\n")
		}
		f.WriteString("// prefixed by go-proto-filename-prefixer to: " + os.Args[2] + originalProtoFile + "\n")

		for _, ll := range lines[originalProtoLine+1 : dataBeginLine] {
			f.WriteString(ll + "\n")
		}

		for len(b) > 0 {
			n := 16
			if n > len(b) {
				n = len(b)
			}

			s := ""
			for _, c := range b[:n] {
				s += fmt.Sprintf("0x%02x, ", c)
			}
			f.WriteString("\t" + strings.TrimSpace(s) + "\n")

			b = b[n:]
		}

		for idx, ll := range lines[dataEndLine:] {
			if idx != len(lines)-dataEndLine-1 {
				f.WriteString(ll + "\n")
			} else {
				f.WriteString(ll)
			}
		}

		f.Close()

		prefixRecord[fromFilename] = toFilename
		outputResult(pbf, fromFilename, toFilename, nil)
		removeFilleByIndex(&pbFiles, fileCursor)

		patchGrpcFile(pbf, fromFilename, toFilename)
	}

}
