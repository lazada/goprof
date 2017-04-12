# Release 1.1.0

 - Added a way of starting separate profiles, so the machinery under every profiling tool doesn't interfere

# Release 1.2.0

 - If it's impossible to collect some profile for specified time interval, then profile is written when you click "start" profiling.
   For example, when heap profile is written, it contains info about heap allocations since last GC until the moment you click "start profiling".
   Now, we write such profiles immediately you click "start profiling"

 - Now we remember date and duration for every profile, so this info is available

 - New profiles was added to UI: goroutines, threadcreate, block

 - Now concurrent downloads don't block each other