# dt-cf-buildpack-integration

Base Library for Cloud Foundry Buildpack integrations with Dynatrace.

## Requirements

- Go 1.11
- Linux to run the tests.

## Development

You can download or clone the repository.

You can run tests through,

```
go test
```

If you modify/add interfaces, you may need to regenerate the mocks. For this you need [gomock](https://github.com/golang/mock):

```
# To download Gomock
go get github.com/golang/mock/gomock
go install github.com/golang/mock/mockgen

# To generate the mocks
go generate
```
