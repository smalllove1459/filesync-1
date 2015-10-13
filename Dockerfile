FROM ubuntu:14.04

RUN apt-get update && apt-get install -y \
    golang

ENV PROJECT_HOME /home/projects/filesync

COPY . $PROJECT_HOME
WORKDIR $PROJECT_HOME
RUN export GOPATH=`pwd` && go install filesync

ENTRYPOINT $PROJECT_HOME/bin/filesync -port=3333
 
EXPOSE 3333
