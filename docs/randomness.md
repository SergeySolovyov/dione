# Randomness in Dione network

“One thing that traditional computer systems aren’t good at is coin flipping,” says Steve Ward, Professor of Computer Science and Engineering at MIT’s Computer Science and Artificial Intelligence Laboratory. 

Computers act deterministically and most of the time calculates a preudo-random numbers.

In Dione network randomness used to calculate the winner of the consensus round on particular epoch. Using pseudo-random values for such operation could break the consensus behaviour and malicious node could predict the next round winners by flipping the numbers over and over.

Since we can't trust preudo-random numbers generated by the node itself we have to find a way to get as random value as possible.

Dione network uses Drand (dee-rand) - distributed randomness beacon. 

You could find more information about Drand using this [link](https://drand.love)