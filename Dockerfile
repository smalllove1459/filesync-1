FROM golang
 
ADD . /go/src/github.com/TapirLiu/filesync
RUN go install github.com/TapirLiu/filesync
ENTRYPOINT /go/bin/filesync -port=3333
 
EXPOSE 3333
