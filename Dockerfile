FROM ubuntu

RUN apt-get update
RUN apt-get install -y git

RUN mkdir /root/.ssh/
ADD id_rsa /root/.ssh/id_rsa
RUN touch /root/.ssh/known_hosts
RUN ssh-keyscan github.com >> /root/.ssh/known_hosts
RUN ssh-keyscan bitbucket.org >> /root/.ssh/known_hosts

RUN \
  mkdir -p /goroot && \
  curl https://storage.googleapis.com/golang/go1.5.1.linux-amd64.tar.gz | tar xvzf - -C /goroot --strip-components=1
ENV GOROOT /goroot
ENV GOPATH /projects/filesync
ENV PATH $GOROOT/bin:$GOPATH/bin:$PATH

RUN mkdir -p /projects \
RUN cd /projects
RUN git clone https://github.com/TapirLiu/filesync.git
RUN cd filesync
RUN go install filesync
RUN bin/filesync

  
