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
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/gorilla/mux"
)

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
	tmpl := template.Must(template.ParseFiles("template.html"))
	err := tmpl.Execute(w, nil)
	if err != nil {
		panic(err)
	}
}

func uploadRawFileHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	vars := mux.Vars(r)
	filename := vars["filename"]
	uploadHandler(w, filename, r.Body)
}

func uploadMultipartFileHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	mpr, err := r.MultipartReader()
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
		uploadHandler(w, part.FileName(), part)
		fmt.Fprintln(w)
	}
}

func logAndWrite(w http.ResponseWriter, format string, params ...interface{}) {
	fmt.Fprintf(w, format+"\n", params...)
	log.Printf(format, params...)
}

func uploadHandler(w http.ResponseWriter, filename string, in io.Reader) {
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
	_, err = io.Copy(outBuf, bufio.NewReader(in))
	if err != nil {
		logAndWrite(w, "%v", err)
		return
	}
	outBuf.Flush()
	logAndWrite(w, "File uploaded successfully : %s", name)
	for i := range hashNames {
		logAndWrite(w, "%s %s", hashNames[i], hex.EncodeToString(hashers[i].(hash.Hash).Sum([]byte{})[:]))
	}
}

var successPath = flag.String("storage", "storage", "The directory where files should be written to")
var certPath = flag.String("cert", "tmp.cert", "The file name of the certificate")
var keyPath = flag.String("key", "tmp.key", "The file name of the key")
var address = flag.String("address", ":5050", "The address:port to listen on, leave address blank for all interfaces")

func init() {
	r := mux.NewRouter()
	r.HandleFunc("/upload/{filename}", uploadRawFileHandler)
	r.HandleFunc("/upload", uploadMultipartFileHandler)
	r.HandleFunc("/", landingPageHandler)
	http.Handle("/", r)
}

func runServer() {
	log.Fatalln("Error starting server", http.ListenAndServeTLS(*address, *certPath, *keyPath, nil))
}

func main() {
	runServer()
}
