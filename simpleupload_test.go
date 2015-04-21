package main

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func init() {
	go runServer()
	time.Sleep(time.Second)
}

func TestClean(t *testing.T) {
	tests := []struct {
		src, want string
	}{
		{"hi", "hi"},
		{"hello.zip", "hello.zip"},
		{"~@#$%^&*()\\/[]}{:;'\",.<>	", "."},
		{"~@#$%-^&_*()", "-_"},
	}
	for _, wg := range tests {
		if w, g := wg.want, cleanName(wg.src); w != g {
			t.Errorf("Wanted %s, got %s", w, g)
		}
	}
}

func TestUpload(t *testing.T) {
	tests := []struct {
		fname  string
		hashes []string
	}{
		{"testfile.test", []string{
			"md5 90089c6ea24b15d7df76f7e1084e553c",
			"sha1 6b40f05b91f578b7d3b44491fe3718a3843f4f70",
			"sha256 42c77eaa4c8161a47f6dc0e5894ecad9282426dd92d2ef366938f19b76c4cd0c",
		}},
	}
	// server is running
	for _, f := range tests {
		nfu, err := newfileUploadRequest(fmt.Sprintf("https://localhost:5050/upload/%s", f.fname), "file", f.fname)
		if err != nil {
			t.Errorf("Error creating upload request - %s", err)
			continue
		}
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		client := &http.Client{Transport: tr}
		resp, err := client.Do(nfu)
		if err != nil {
			t.Errorf("Error with POST request - %s", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected %d, not %d", http.StatusOK, resp.StatusCode)
		}
		cont, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Errorf("Error reading response body - %s", err)
			continue
		}

		res := string(cont)
		if !strings.Contains(res, "File uploaded successfully : ") {
			t.Errorf("File upload was not a success, got instead: %s", res)
			continue
		}
		splt := strings.Split(res, "\n")
		for x := range f.hashes {
			if len(splt) <= x {
				t.Errorf("Output from upload did not display the proper number of hashes")
				continue
			}
			if g, w := splt[x+2], f.hashes[x]; w != g {
				t.Errorf("Wanted %s, got %s", w, g)
			}
		}
	}
}

func newfileUploadRequest(uri string, paramName, path string) (*http.Request, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return http.NewRequest("PUT", uri, file)
}
