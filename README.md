Given a YAML file as input for `PipelineParams` in [_examples/simple.yaml](./_examples/simple.yaml), the program should be able to start a pipeline.
Use [development containers](https://containers.dev) (see [.devcontainer](./.devcontainer)).

Start a worker with:
```sh
go run . worker
```
Then open another terminal session and queue a workflow execution with:

```sh
WORKFLOW_INPUT=_examples/simple.yaml go run . pipeline
```

You should be able to see the Temporal Web UI at [http://localhost:6434](http://localhost:6434).