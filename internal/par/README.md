# par

Bounded parallel work for the stages and adapters: run a function over a list with at most N calls
in flight, and on the first error cancel the rest and return it.

Use it in place of hand-rolled goroutine and semaphore code, so every stage cancels the same way.
