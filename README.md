[![GoDoc](https://godoc.org/github.com/Galaco/sourcenet?status.svg)](https://godoc.org/github.com/Galaco/sourcenet)
[![Go report card](https://goreportcard.com/badge/github.com/galaco/sourcenet)](https://goreportcard.com/badge/github.com/galaco/sourcenet)
[![GolangCI](https://golangci.com/badges/github.com/galaco/sourcenet.svg)](https://golangci.com)
[![Build Status](https://travis-ci.com/Galaco/sourcenet.svg?branch=master)](https://travis-ci.com/Galaco/sourcenet)
[![CircleCI](https://circleci.com/gh/Galaco/sourcenet.svg?style=svg)](https://circleci.com/gh/Galaco/sourcenet)

# SourceNet

A Source Engine multiplayer client netcode implementation in Golang. This is very incomplete, and is based primarily on
reverse engineering cs:source packet data, with a little help from the Source Engine code leaks (also see credits).


### Getting started
##### Prerequisites
* Steam must be running, or steamworks will have issues.
* `steam_api64.dll` must be obtained from Steamworks SDK, and placed in
this directory.
* Create a file named `steam_appid.txt`. Contents should be only the
Source Engine game appid you are acting as (e.g. Counterstrike: Source
has appid 240).


### Examples
There are 2 examples currently.
* Query public info from servers. This is the same information that
you see on the Steam server browser listing.
* Initial client authentication and handshake. This example will progress
to the point where the server begins to send packets to the client of
its own accord.



### Credits
A lot of this wouldn't have been possible without the work of leystryku: [https://github.com/Leystryku/leysourceengineclient](https://github.com/Leystryku/leysourceengineclient)