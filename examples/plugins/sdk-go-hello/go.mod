// SDK-based hello-world plugin module.
//
// Imports the SDK at packages/go/sdk via a relative replace
// directive so a tinygo build pulls the SDK from the same repo. Real
// plugins ship a module that depends on a tagged SDK release.

module github.com/Singleton-Solution/GoNext/examples/plugins/sdk-go-hello

go 1.25.0

require github.com/Singleton-Solution/GoNext/packages/go/sdk v0.0.0

replace github.com/Singleton-Solution/GoNext/packages/go/sdk => ../../../packages/go/sdk
