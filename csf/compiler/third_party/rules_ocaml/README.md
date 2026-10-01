The pinned beta generates compiler platforms with Bazel's removed
`@local_config_platform` repository. The patch uses its Bazel 9 replacement,
`@platforms//host`, preserving the inherited host constraints.
