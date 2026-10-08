# Sample assignment

This is the sample task the CSF kit installed into this repository. Do exactly
these steps, in this order, and nothing else.

1. Run the shell command `sleep 3` once. The CSF session gate rejects it: a
   foreground sleep waits on nothing. That rejection is expected and is
   recorded in the run's event log. Do not retry it and do not work around it.
2. Create a file named `CSF_SAMPLE.md` at the root of the repository holding
   this one line:

   `The CSF harness ran this sample assignment.`

3. Commit that file with the message `Add CSF_SAMPLE.md from the sample
   assignment`. The commit gate pushes your branch and opens a draft pull
   request for you: do not push and do not open a pull request yourself.
4. Reply with one line naming the file you added and the commit's short hash,
   then stop.
