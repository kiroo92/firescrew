FROM docker.cattt.net/golang:latest AS firescrew-build-stage
WORKDIR /build
COPY . .
RUN go env -w GO111MODULE=on
RUN go env -w GOPROXY=https://goproxy.cn,direct
RUN ls -la; go mod download
RUN mkdir /bins
RUN GOOS=linux GOARCH=amd64 go build -ldflags "-X 'main.Version=$(date +'%Y-%m-%d_%H:%M:%S')'" -o /bins/firescrew.linux.amd64
#RUN GOOS=linux GOARCH=arm64 go build -ldflags "-X 'main.Version=$(date +'%Y-%m-%d_%H:%M:%S')'" -o /bins/firescrew.linux.arm64
#RUN GOOS=linux GOARCH=arm64 go build -ldflags "-X 'main.Version=$(date +'%Y-%m-%d_%H:%M:%S')'" -o /bins/firescrew.linux.arm64
RUN cd demoStream/rtspServer; GOOS=linux GOARCH=amd64 go build -ldflags "-X 'main.Version=$(date +'%Y-%m-%d_%H:%M:%S')'" -o /bins/rtspServer.linux.amd64
#RUN cd demoStream/rtspServer; GOOS=linux GOARCH=arm64 go build -ldflags "-X 'main.Version=$(date +'%Y-%m-%d_%H:%M:%S')'" -o /bins/rtspServer.linux.arm64
#RUN cd demoStream/rtspServer; GOOS=linux GOARCH=arm64 go build -ldflags "-X 'main.Version=$(date +'%Y-%m-%d_%H:%M:%S')'" -o /bins/rtspServer.linux.arm64


FROM docker.cattt.net/kklt06/firescrew:base
COPY --from=firescrew-build-stage /bins /bins
COPY --from=firescrew-build-stage /build/docker/run-firescrew.sh /run-firescrew.sh
# RUN ls -la /
# RUN ls -la /run-firescrew.sh

# RUN head -1 /run-firescrew.sh | od -c  

RUN sed -i 's/\r$//' /run-firescrew.sh
RUN chmod +x /run-firescrew.sh
ENTRYPOINT ["/run-firescrew.sh"]
CMD []