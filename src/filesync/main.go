// Run (default port is 80):
//    filesync -port=80
//
// To sync a new file:
//    curl -X POST -d remote=remote-server:port/pull -d file=path-to-file http://filesync-server:port/sync
// For example:
//    curl -X POST -d remote=secure-appldnld.apple.com/itunes12 -d file=031-36659-20150924-CE836C92-630F-11E5-8BF6-43BB51D92A8C/iTunes6464Setup.exe  http://filesync-server:port/sync
//
// Followings are not supported now:
//    authentication
//    cryptography, not support https
//    file hash and file database
//    delete files
//    多点传输
//
package main

import "flag"

func main() {
	port := flag.Int("port", 80, "server ports")
	flag.Parse()
	new(Server).start(*port)
}
