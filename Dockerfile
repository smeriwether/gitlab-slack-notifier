FROM alpine
RUN apk --update add ca-certificates
COPY gitlab-bot /opt/
ENTRYPOINT ["/opt/gitlab-bot"]
