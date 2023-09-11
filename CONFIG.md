# Notes

* added `internal/hclconfig` package as an interface for low level configuration

# Breaking Changes

## Propositions

* implementation detail: dont use nested objects in "leaf" configurations, it should be the responsibility of the configuration provisioner to flatten inline objects into the configuration graph

# TODO

* [ ] add hcl config thta compiles to low level json config @apparentlymarts previous work
* [ ] add documentation for the new configuration scheme
* [ ] add convenience tooling to convert old configurations to new scheme
* [ ] handle client configuration
