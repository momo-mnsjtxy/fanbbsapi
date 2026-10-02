// Worker placeholder makes the process boundary explicit while integrations are off.
package main

import "log"

func main() {
	log.Print("FanBBS worker has no enabled external jobs in the local vertical slice")
}
