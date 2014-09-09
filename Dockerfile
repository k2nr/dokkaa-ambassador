FROM crosbymichael/golang
MAINTAINER Kazunori Kajihiro <likerichie@gmail.com> (@k2nr)

ADD . /go/src/github.com/k2nr/dokkaa-ambassador/
RUN go get github.com/k2nr/dokkaa-ambassador

ENTRYPOINT ["dokkaa-ambassador"]
