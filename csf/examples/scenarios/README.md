# Simulator scenario projections

`project.py` reads the generated `Scenario` protobuf JSON and produces a concrete
CARLA or Isaac **plan**. The output carries realized initial conditions, physical
units, frame conversion, clock, controller normalization, required observables,
oracles and unresolved execution requirements. It does not start a simulator.

From the sibling `training` directory, after generating protobuf bindings:

```sh
uv run --locked python ../scenarios/project.py \
  --scenario /absolute/path/to/straight-scenario.json \
  --backend carla --output /absolute/path/to/carla-plan.json
uv run --locked python ../scenarios/project.py \
  --scenario /absolute/path/to/straight-scenario.json \
  --backend isaac --output /absolute/path/to/isaac-plan.json
```

The first projection profile accepts straight paths without faults. It rejects
curvature, injected faults, unknown fields, nonfinite physical values and unknown
fault kinds. It never silently drops a requirement. Fault injection currently
executes only in the CPU training harness.

Execution still requires a pinned simulator, a matching lane and vehicle fixture,
an actuator mapping with measured tolerances, and an adapter conformance run.
CARLA's normalized steering command and Isaac's joint targets cannot be equated
with a physical steering angle or acceleration without that mapping. A seed and
an identical scenario specification do not establish identical physics across
engines. No CARLA/Isaac execution, imported vehicle asset, ROS adapter or physical
robot is delivered by this generator.

The reference versions are CARLA 0.9.16 and the direct Isaac Sim 6.0 interface.
The initial translation keeps these choices explicit rather than pretending to
support every API or simulator version. See [CARLA synchronization](https://carla.readthedocs.io/en/latest/adv_synchrony_timestep/)
and [Isaac conventions](https://docs.isaacsim.omniverse.nvidia.com/latest/reference_material/sim_conventions.html).
