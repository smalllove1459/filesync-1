package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// FileSavePath and FileSyncingPath must be in the same partition (and on Linux OS), otherwise os.Rename will fail.
	FileSavePath        = "/var/lib/filesync-save"
	FileSyncingPath     = "/var/lib/filesync-sync"
	FileSyncingInfoPath = "/var/lib/filesync-info"

	MaxNumRetries = 3

	UrlPrefix_Sync = "/sync" // as receiver
	UrlPrefix_Pull = "/pull" // as sender. This is just for using this program as a file server.
)

///===========================================================================
/// file
///===========================================================================

type FileInfo struct {
	fullPath   string
	size       int64
	modifyTime time.Time
}

type FileSnycingInfo struct {
	ContentLength int64
	LastModified  time.Time // UTC
}

type TargetFileInfo struct {
	saveFileInfo *FileInfo

	// the blow two must be null or non-null at the same time.
	// if they are not null, they will surpress saveFileInfo.

	syncingFileInfo *FileInfo
	fileSyncingInfo *FileSnycingInfo
}

func validateFilePath(filePath string) string {
	if strings.HasPrefix(filePath, "/") {
		return filePath
	} else {
		return fmt.Sprintf("/%s", filePath)
	}
}

func getFilleFullPath(path string, file string) string {
	return filepath.FromSlash(fmt.Sprintf("%s%s", path, file))
}

func getFileInfo(fullFilePath string) (*FileInfo, error) {
	var info, err = os.Stat(fullFilePath)

	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory.", fullFilePath)
	}

	return &FileInfo{
		fullPath:   fullFilePath,
		size:       info.Size(),
		modifyTime: info.ModTime().UTC(),
	}, nil
}

func (myRequest *MyRequestToRemote) getTargetFileInfo(filePath string) (*TargetFileInfo, error) {
	var save_file_info *FileInfo
	var syncing_file_info *FileInfo

	var err error

	save_file_info, err = getFileInfo(getFilleFullPath(FileSavePath, filePath))

	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	syncing_file_info, err = getFileInfo(getFilleFullPath(FileSyncingPath, filePath))

	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	syncing_info_file_path := getFilleFullPath(FileSyncingInfoPath, filePath)
	syncing_info := readFileSyncingInfo(syncing_info_file_path)

	if syncing_info == nil && syncing_file_info != nil {
		myRequest.removeFile(syncing_file_info.fullPath)
		syncing_file_info = nil
	} else if syncing_info != nil && syncing_file_info == nil {
		myRequest.removeFile(syncing_info_file_path)
		syncing_info = nil
	}

	if syncing_info != nil && save_file_info != nil && syncing_info.LastModified.Before(save_file_info.modifyTime) {
		myRequest.removeFile(syncing_file_info.fullPath)
		myRequest.removeFile(syncing_info_file_path)
		syncing_file_info = nil
		syncing_info = nil
	}

	return &TargetFileInfo{
		saveFileInfo:    save_file_info,
		syncingFileInfo: syncing_file_info,
		fileSyncingInfo: syncing_info,
	}, nil
}

func readFileSyncingInfo(fullFilePath string) *FileSnycingInfo {
	file, err := ioutil.ReadFile(fullFilePath)
	if err != nil {
		return nil
	}

	info := &FileSnycingInfo{}
	err = json.Unmarshal(file, info)
	if err != nil {
		return nil
	}

	return info
}

func writeFileSyncingInfo(fullFilePath string, info *FileSnycingInfo) error {
	err := makeSureParentFolderCreated(fullFilePath)
	if err != nil {
		return err
	}

	data, err := json.Marshal(info)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(fullFilePath, data, 0644)
	if err != nil {
		return err
	}

	return nil
}

func writeFileFromReader(reader io.Reader, fullFilePath string, isAppend bool) (int64, error) {
	err := makeSureParentFolderCreated(fullFilePath)
	if err != nil {
		return 0, err
	}

	var out *os.File
	if isAppend {
		out, err = os.OpenFile(fullFilePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return 0, err
		}
	} else {
		out, err = os.OpenFile(fullFilePath, os.O_TRUNC|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return 0, err
		}
	}

	defer out.Close()

	return io.Copy(out, reader)
}

func makeSureParentFolderCreated(fullFilePath string) error {
	return os.MkdirAll(filepath.Dir(fullFilePath), 0777)
}

func changeFileModifyTime(fullFilePath string, utcTime time.Time) error {
	local_time := utcTime.Local()
	return os.Chtimes(fullFilePath, local_time, local_time)
}

func (myRequest *MyRequestToRemote) removeFile(fullFilePath string) error {
	var index = strings.IndexByte(myRequest.filePath[1:], '/') // shouldn't use filepath.Separator
	if index < 0 {
		return os.Remove(fullFilePath)
	}
	index++ // ++ for [1:]

	index = len(fullFilePath) - len(myRequest.filePath) + index

	os.Remove(fullFilePath) // on windows, non empty folder can't be removed.

	return os.Remove(fullFilePath[:index])
}

func (myRequest *MyRequestToRemote) moveFile(fromFilePath string, toFilePath string) error {
	if filepath.Separator == '\\' {
		myRequest.removeFile(toFilePath) // windows can't override existed files
	}

	err := makeSureParentFolderCreated(toFilePath)
	if err != nil {
		return err
	}

	err = os.Rename(fromFilePath, toFilePath)
	if err != nil {
		return err
	}

	return myRequest.removeFile(fromFilePath)
}

///===========================================================================
///
///===========================================================================

type ContentRange struct {
	start          int
	length         int
	expectedLength int
}

func perseContentRange(contentRange string) *ContentRange {
	if range_str := strings.TrimPrefix(contentRange, "bytes "); range_str != contentRange {
		if p1 := strings.IndexByte(range_str, '-'); p1 > 0 {
			if p2 := strings.IndexByte(range_str[p1:], '/'); p2 > 0 {
				p2 += p1
				start, err1 := strconv.Atoi(range_str[:p1])
				length, err2 := strconv.Atoi(range_str[p1+1 : p2])
				expected_length, err3 := strconv.Atoi(range_str[p2+1:])
				if err3 != nil {
					err3 = nil
					expected_length = 0 // means unknown
				}

				if err1 == nil && err2 == nil && err3 == nil {
					return &ContentRange{start: start, length: length, expectedLength: expected_length}
				}
			}
		}
	}

	return nil
}

///===========================================================================
/// pull files
///===========================================================================

type MyRequestToRemote struct {
	server *Server

	remoteSerever string
	filePath      string

	fileSize      int64
	downloaded    int64
	newDownloaded int64

	targetFileInfo *TargetFileInfo

	shouldHandle chan bool
}

func (myRequest *MyRequestToRemote) getDownloadProgressString() string {
	//downloads := atomic.LoadInt64 (&myRequest.downloaded)
	fileSize := atomic.LoadInt64(&myRequest.fileSize)

	var percentage string
	if fileSize <= 0 {
		percentage = "0% downloaded"
	} else {
		percentage = "downloading" // todo: strconv.FormatFloat(float64 (downloads) * 100.0 / float64 (fileSize), 'f', 2, 32) + "%"
	}

	return percentage
}

func (myRequest *MyRequestToRemote) removeTempFiles() {
	myRequest.removeFile(getFilleFullPath(FileSyncingPath, myRequest.filePath))
	myRequest.removeFile(getFilleFullPath(FileSyncingInfoPath, myRequest.filePath))
}

func (myRequest *MyRequestToRemote) error(msg string, fatal bool) {
	log.Printf("Error on file (%s) syncing: %s. Download new %d bytes.\n", myRequest.filePath, msg, myRequest.newDownloaded)
	if fatal {
		myRequest.removeTempFiles()
	}

	myRequest.close()
}

func (myRequest *MyRequestToRemote) ok(justFinished bool, utcTime time.Time) {
	if justFinished {
		myRequest.moveFile(getFilleFullPath(FileSyncingPath, myRequest.filePath), getFilleFullPath(FileSavePath, myRequest.filePath))
		myRequest.removeTempFiles()
		changeFileModifyTime(getFilleFullPath(FileSavePath, myRequest.filePath), utcTime)

		log.Printf("Finished file (%s) syncing. Download new %d bytes.\n", myRequest.filePath, myRequest.newDownloaded)
	} else {
		log.Printf("Stopped file (%s) syncing. Download new %d bytes.\n", myRequest.filePath, myRequest.newDownloaded)
	}

	myRequest.close()
}

func (myRequest *MyRequestToRemote) close() {
	myRequest.server.doneMyRequests <- myRequest
}

func (myRequest *MyRequestToRemote) run() {
	num_retries := 0
	
	for myRequest.doDownload () && num_retries <= MaxNumRetries {
		num_retries++
		
		myRequest.targetFileInfo, _ = myRequest.getTargetFileInfo(myRequest.filePath)
		if myRequest.targetFileInfo == nil {
			myRequest.error(fmt.Sprintf("Failed to retry (%d) download file: %s", num_retries, myRequest.filePath), false)
			reurn
		}
		
		log.Printf("Retry (%d) download file: %s", num_retries, myRequest.filePath)
	}
}

func (myRequest *MyRequestToRemote) doDownload () bool {
	target_file_info := myRequest.targetFileInfo

	remote_url := fmt.Sprintf("http://%s%s", myRequest.remoteSerever, validateFilePath(myRequest.filePath))

	request, err := http.NewRequest("POST", remote_url, nil)
	if err != nil {
		myRequest.error(err.Error(), false)
		return false
	}

	if target_file_info.syncingFileInfo != nil { // && target_file_info.fileSyncingInfo != nil
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", target_file_info.syncingFileInfo.size))
		request.Header.Set("If-Range", target_file_info.fileSyncingInfo.LastModified.UTC().Format(http.TimeFormat))
	} else if target_file_info.saveFileInfo != nil {
		request.Header.Set("If-Modified-Since", target_file_info.saveFileInfo.modifyTime.UTC().Format(http.TimeFormat))
	}

	client := &http.Client{
		Timeout: 0, // 0 means no timeout
	}

	response, err := client.Do(request)
	if err != nil {
		myRequest.error(err.Error(), false)
		return false
	}

	defer response.Body.Close()

	if response.StatusCode == http.StatusNotModified { // 304
		log.Printf("Response: file not changed.\n")

		// assert target_file_info.saveFileInfo != nil

		myRequest.removeTempFiles()

		myRequest.ok(false, time.Time{})

	} else if response.StatusCode == http.StatusPartialContent { // 206
		log.Printf("Response: downloading resumed.\n")

		if target_file_info.syncingFileInfo == nil || target_file_info.fileSyncingInfo == nil {
			myRequest.error("syncingFileInfo and fileSyncingInfo shouldn't be null", false)
			return false
		}
		myRequest.fileSize = int64(target_file_info.fileSyncingInfo.ContentLength)
		myRequest.downloaded = int64(target_file_info.syncingFileInfo.size)

		content_range := perseContentRange(response.Header.Get("Content-Range"))
		if content_range == nil {
			myRequest.error(fmt.Sprintf("Parse error (%s) in syncing file: %s", response.Header.Get("Content-Range"), myRequest.filePath), false)
			return false
		}

		if int64(content_range.start) != target_file_info.syncingFileInfo.size {
			myRequest.error(fmt.Sprintf("Content-Range start (%d) doesn't math current file size (%d).", content_range.start, target_file_info.syncingFileInfo.size), false)
			return false
		}

		content_length, err := strconv.Atoi(response.Header.Get("Content-Length"))
		if err != nil {
			myRequest.error(err.Error(), false)
			return false
		}

		num_bytes, err := writeFileFromReader(response.Body, getFilleFullPath(FileSyncingPath, myRequest.filePath), true)
		myRequest.downloaded += num_bytes
		myRequest.newDownloaded += num_bytes

		//if err != nil {
		//	myRequest.error(err.Error(), false)
		//	return false
		//}
		if num_bytes > int64(content_length) {
			myRequest.error(fmt.Sprintf("Error: num_bytes > content_length in syncing file: %s", myRequest.filePath), true)
			return false
		}

		new_size := int64(num_bytes) + target_file_info.syncingFileInfo.size
		if new_size > target_file_info.fileSyncingInfo.ContentLength {
			myRequest.error(fmt.Sprintf("Error: new_size > target_file_info..fileSyncingInfo.ContentLength: %s", myRequest.filePath), true)
			return false
		}

		if num_bytes == int64(content_length) && new_size == target_file_info.fileSyncingInfo.ContentLength {
			myRequest.ok(true, target_file_info.fileSyncingInfo.LastModified)
		} else {
			return true // retry
		}

	} else if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusRequestedRangeNotSatisfiable { // 200 or 416
		log.Printf("Response: send file from scratch.\n")

		myRequest.removeTempFiles()

		content_length, err := strconv.Atoi(response.Header.Get("Content-Length"))
		if err != nil {
			myRequest.error(err.Error(), false)
			return false
		}
		myRequest.fileSize = int64(content_length)
		myRequest.downloaded = 0

		last_modified, err := time.Parse(http.TimeFormat, response.Header.Get("Last-Modified"))
		if err != nil {
			myRequest.error(err.Error(), false)
			return false
		}

		syncing_info := &FileSnycingInfo{
			ContentLength: int64(content_length),
			LastModified:  last_modified,
		}
		writeFileSyncingInfo(getFilleFullPath(FileSyncingInfoPath, myRequest.filePath), syncing_info)

		num_bytes, err := writeFileFromReader(response.Body, getFilleFullPath(FileSyncingPath, myRequest.filePath), false)
		myRequest.downloaded += num_bytes
		myRequest.newDownloaded += num_bytes

		//if err != nil {
		//	myRequest.error( err.Error(), false)
		//	return false
		//}
		if num_bytes > int64(content_length) {
			myRequest.error( fmt.Sprintf("Error: num_bytes > content_length in syncing file: %s", myRequest.filePath), true)
			return false
		}

		if num_bytes == int64(content_length) {
			myRequest.ok( true, last_modified)
		} else {
			return true // retry
		}

	} else { // unsupported
		myRequest.error( fmt.Sprintf("Unsupported response status code: %d", response.StatusCode), false)
	}
	
	return false
}

///===========================================================================
/// http handlers
///===========================================================================

func (server *Server) syncHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	file_path := r.FormValue("file")
	if file_path == "" {
		w.Write([]byte("Please set remote file: -d file=path/to/file"))
		return
	}
	file_path = validateFilePath(file_path)

	// ...

	var query = &DownloadPercentageQuery{filePath: file_path, result: make(chan string, 1)}
	server.downloadPercentageQueries <- query
	var result = <-query.result
	if result != "" {
		w.Write([]byte(result))
		return
	}

	// ...

	remote_server := r.FormValue("remote")
	if remote_server == "" {
		w.Write([]byte("Please set remote server: -d remote=remote_server"))
		return
	}
	if strings.HasSuffix(remote_server, "/") {
		remote_server = remote_server[:len(remote_server)-1]
	}

	// ...

	my_request := &MyRequestToRemote{
		server:        server,
		remoteSerever: remote_server,
		filePath:      file_path,
	}

	target_file_info, err := my_request.getTargetFileInfo(file_path)
	if err != nil {
		w.Write([]byte(fmt.Sprintf("Error to get target info: %s", err)))
		return
	}

	my_request.targetFileInfo = target_file_info
	my_request.shouldHandle = make(chan bool, 1)

	server.newMyRequests <- my_request

	if <-my_request.shouldHandle {
		go my_request.run()
		w.Write([]byte("Downloading started."))
	} else {
		w.Write([]byte("For some reason, the file is denied to synchronize."))
	}
}

///===========================================================================
/// server
///===========================================================================

type DownloadPercentageQuery struct {
	filePath string
	result   chan string
}

type Server struct {
	// as receiver
	newMyRequests    chan *MyRequestToRemote
	doneMyRequests   chan *MyRequestToRemote
	activeMyRequests map[string]*MyRequestToRemote

	// query
	downloadPercentageQueries chan *DownloadPercentageQuery
}

func (server *Server) run() {
	for {
		select {
		case my_request := <-server.newMyRequests:

			if server.activeMyRequests[my_request.filePath] == nil {
				server.activeMyRequests[my_request.filePath] = my_request
				my_request.shouldHandle <- true
			} else {
				my_request.shouldHandle <- false
			}
		case my_request := <-server.doneMyRequests:

			delete(server.activeMyRequests, my_request.filePath)

		case query := <-server.downloadPercentageQueries:

			my_request := server.activeMyRequests[query.filePath]
			if my_request == nil {
				query.result <- ""
			} else {
				query.result <- my_request.getDownloadProgressString()
			}

		}
	}
}

func (server *Server) start(port int) {
	server.newMyRequests = make(chan *MyRequestToRemote, 100)
	server.doneMyRequests = make(chan *MyRequestToRemote, 100)
	server.activeMyRequests = make(map[string]*MyRequestToRemote)

	server.downloadPercentageQueries = make(chan *DownloadPercentageQuery, 5)

	// ...

	go server.run()

	// as sender
	http.Handle(UrlPrefix_Pull+"/", http.StripPrefix(UrlPrefix_Pull+"/", http.FileServer(http.Dir(FileSavePath))))

	// as receiver
	http.HandleFunc(UrlPrefix_Sync, server.syncHandler)

	// ...
	var address = fmt.Sprintf(":%d", port)

	http_server := http.Server{
		Addr:         address,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
	}
	log.Printf("Server is listening at %s ...\n", address)
	log.Fatal(http_server.ListenAndServe())
}
