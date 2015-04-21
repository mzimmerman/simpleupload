package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"hash"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/pivotal-golang/bytefmt"
)

var transferCount uint64
var transferStatusChan = make(chan TransferStatus, 1024) // a large chan buffer to not slow up transfers
var transferStatusReq = make(chan (chan TransferStatus))

var cleanRegex = regexp.MustCompile("[a-zA-Z\\.\\-_]")

func cleanName(src string) string {
	var buf bytes.Buffer
	for _, s := range src {
		if cleanRegex.MatchString(string(s)) {
			buf.WriteRune(s)
		}
	}
	return buf.String()
}

func landingPageHandler(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		logAndWrite(w, "Error parsing remote address - %s - %v", r.RemoteAddr, err)
	}
	srcIP := net.ParseIP(host)
	tmpl := template.Must(template.ParseFiles("template.html"))
	err = tmpl.Execute(w, getTransferStatus(srcIP))
	if err != nil {
		panic(err)
	}
}

func uploadRawFileHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	vars := mux.Vars(r)
	filename := vars["filename"]
	uploadHandler(w, r, filename, r.Body)
}

func uploadMultipartFileHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	mpr, err := r.MultipartReader()
	// TODO log specific error to client about forgetting filename when using curl
	if err != nil {
		logAndWrite(w, "Error creating multipart reader - %v", err)
		return
	}
	for {
		part, err := mpr.NextPart()
		if err == io.EOF {
			return
		}
		defer part.Close()
		if part.FileName() == "" {
			continue
		}
		uploadHandler(w, r, part.FileName(), part)
		fmt.Fprintln(w)
	}
}

func logAndWrite(w http.ResponseWriter, format string, params ...interface{}) {
	fmt.Fprintf(w, format+"\n", params...)
	log.Printf(format, params...)
}

func uploadHandler(w http.ResponseWriter, r *http.Request, filename string, in io.Reader) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		logAndWrite(w, "Error parsing remote address - %s - %v", r.RemoteAddr, err)
	}
	srcIP := net.ParseIP(host)
	dateName := time.Now().Format("2006-01-02_15-04-05")
	name := cleanName(filename)
	out, err := os.Create(fmt.Sprintf("%s%c%s_%s", *successPath, os.PathSeparator, name, dateName))
	if err != nil {
		logAndWrite(w, "Unable to create the file for writing. Check your write access privilege")
		return
	}
	defer out.Close()
	log.Printf("Writing to filename - %s", out.Name())
	hashNames := []string{"md5", "sha1", "sha256"}
	hashers := []io.Writer{md5.New(), sha1.New(), sha256.New()}
	mw := io.MultiWriter(append([]io.Writer{out}, hashers...)...)
	outBuf := bufio.NewWriter(mw)
	buf := make([]byte, 32*1024)
	ts := TransferStatus{
		Index:    atomic.AddUint64(&transferCount, 1), // increment by 1
		Filename: filename,
		Size:     0,
		Start:    time.Now(),
		//		End:    zero time
		Source: srcIP,
	}
	transferStatusChan <- ts
	for {
		nr, err := in.Read(buf)
		if nr > 0 {
			ts.Size += uint64(nr)
			transferStatusChan <- ts
			nw, err := outBuf.Write(buf[0:nr])
			if err != nil {
				ts.End = time.Now()
				transferStatusChan <- ts
				logAndWrite(w, "Error writing to file - %v", err)
				return
			}
			if nr != nw {
				ts.End = time.Now()
				transferStatusChan <- ts
				logAndWrite(w, "Error writing out all the data that was read")
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			ts.End = time.Now()
			transferStatusChan <- ts
			logAndWrite(w, "Error reading the file - %v", err)
			return
		}
	}
	outBuf.Flush()
	ts.End = time.Now()
	http.Redirect(w, r, "/", http.StatusFound)
	logAndWrite(w, "File uploaded successfully : %s", name)
	logAndWrite(w, "Transfer stats : %s", ts)
	for i := range hashNames {
		line := fmt.Sprintf("%s %s", hashNames[i], hex.EncodeToString(hashers[i].(hash.Hash).Sum([]byte{})[:]))
		ts.Hashes = append(ts.Hashes, line)
		logAndWrite(w, line)
	}
	transferStatusChan <- ts
}

var successPath = flag.String("storage", "storage", "The directory where files should be written to")
var certPath = flag.String("cert", "tmp.cert", "The file name of the certificate")
var keyPath = flag.String("key", "tmp.key", "The file name of the certificate's private key")
var address = flag.String("address", ":5050", "The address:port to listen on, leave address blank for all interfaces")

func init() {
	flag.Parse()
	r := mux.NewRouter()
	r.HandleFunc("/upload/{filename}", uploadRawFileHandler)
	r.HandleFunc("/upload", uploadMultipartFileHandler)
	r.HandleFunc("/", landingPageHandler)
	http.Handle("/", r)
}

func getTransferStatus(src net.IP) chan TransferStatus {
	c := make(chan TransferStatus)
	transferStatusReq <- c
	c <- TransferStatus{Source: src}
	return c
}

func runServer() {
	tsman := make([]TransferStatus, 0)
	ticker := time.Tick(time.Second * 10)
	go func() {
	NextChan:
		for {
			select {
			case tsreq := <-transferStatusReq:
				ts := <-tsreq
				for x := range tsman {
					if tsman[x].Source.Equal(ts.Source) {
						tsreq <- tsman[x]
					}
				}
				close(tsreq)
			case ts := <-transferStatusChan:
				for x := range tsman {
					if tsman[x].Index == ts.Index {
						tsman[x] = ts
						continue NextChan
					}
				}
				log.Printf("Transfer started - %s", ts)
				tsman = append(tsman, ts)
			case <-ticker:
				for x := 0; x < len(tsman); x++ {
					if !tsman[x].End.IsZero() {
						continue
					}
					log.Print(tsman[x])
				}
			case now := <-time.After(time.Minute):
				for x := 0; x < len(tsman); x++ {
					if tsman[x].End.IsZero() {
						continue
					}
					if now.Sub(tsman[x].End) > time.Minute*5 {
						tsman[x], tsman = tsman[len(tsman)-1], tsman[:len(tsman)-1]
						x--
					}
				}
			}
		}
	}()
	log.Fatalln("Error starting server", http.ListenAndServeTLS(*address, *certPath, *keyPath, nil))
}

func main() {
	runServer()
}

type TransferStatus struct {
	Index    uint64
	Filename string
	Size     uint64
	Start    time.Time
	End      time.Time
	Source   net.IP
	Hashes   []string
}

func (ts TransferStatus) HumanSize() string {
	return bytefmt.ByteSize(ts.Size)
}

func (ts TransferStatus) Speed() string {
	if ts.End.IsZero() {
		ts.End = time.Now() // act like it's finished
	}
	duration := ts.End.Sub(ts.Start)
	bytesPerSecond := ts.Size * 1000000000 / uint64(duration.Nanoseconds())
	return bytefmt.ByteSize(bytesPerSecond)
}

func (ts TransferStatus) String() string {
	var result bytes.Buffer
	if ts.End.IsZero() {
		ts.End = time.Now() // act like it's finished
	}
	duration := ts.End.Sub(ts.Start)
	result.WriteString(fmt.Sprintf("%d - done=%t, %s %s in %s for speed of %s/s", ts.Index, !ts.End.IsZero(), ts.Filename, ts.HumanSize(), duration, ts.Speed()))
	for _, l := range ts.Hashes {
		result.WriteString("\n")
		result.WriteString(l)
	}
	return result.String()
}
