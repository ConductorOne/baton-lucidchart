FROM gcr.io/distroless/static-debian11:nonroot
ENTRYPOINT ["/baton-lucidchart"]
COPY baton-lucidchart /