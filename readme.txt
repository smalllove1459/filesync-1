Sync server files remotely.

Build：
   cd <project_dir>
   export GOPATH=`pwd`
   go install filesync

Run (default port is 80):
    bin/filesync -port=80

The filesync server acts both as file sender and file receiver.
    
To sync a new file:
    curl -X POST -d remote=sender-filesync-server:port/pull -d file=path-to-file http://receiver-filesync-server:port/sync

The receiver server can also download files from other websites (some websites may not support custom client programs). Here is an example to download iTunes from Apple website:
    curl -X POST -d remote=secure-appldnld.apple.com/itunes12 -d file=031-36659-20150924-CE836C92-630F-11E5-8BF6-43BB51D92A8C/iTunes6464Setup.exe  http://receiver-filesync-server:port/sync

The program supports downloading resume.
Followings are not supported now:
    authentication
    cryptography, not support https
    file hash and file database
    delete files
    多点传输
