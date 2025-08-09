# Story: Example KV Store

Distributed key/value store which demonstrates the use of our raft go library.

## Goal

As a developer, I want to be able to use our raft library within my application to maintain data consistency across servers.
To getting started, I like to have a simple example which demonstrates the use of the library.

For this story, we will build a simple key/value store which can be used to demonstrate the use of the library.

## Acceptance Criteria

* A README.md file which explains how to run the example.
* A working example of a distributed key/value store.
  * The key/value store binary starts a single instance of the key/value store server.
  * The key/value store binary supports the following command line arguments:
    * --listener: The host:port to listen on for client connections.
    * --peers: A comma separated list of host:port pairs for the peers in the cluster.
    * --data-dir: The directory to store the data.
    * --help: Print the help message and exit.
  * The key/value store binary should print out the endpoint of the server when it starts.
  * The key/value store supports static discovery of servers.
  * The key/value store should be able to store, delete and retrieve values.
  * The key/value store provides a REST API for interacting with the key/value store.
  * The key/value store provides a status endpoint which provides information about the cluster.
* A Python script which builds the key/value store starts a cluster of servers and then runs a simple test. 
  * The Python script provides configuration options for the cluster size, to enable the test run and the number of clients used in the test run.
  * The Python script prints out the endpoint with usage examples when the test run is disabled.
  * The Python script blocks until it is closed when the test run is disabled.
  * The Python script stops the cluster of servers when it is done.
  * The Python script runs tests which verify the key/value store is working as expected.

## Remarks

* The key/value store implementation should follow clean code principles.
* The key/value store implementation should be tested.