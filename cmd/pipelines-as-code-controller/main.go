package main

import (
	"context"
	"log"

	"knative.dev/pkg/signals"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/adapter"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
)

const (
	PACControllerLogKey = "pipelinesascode"
)

func main() {
	ctx := signals.NewContext()

	run := params.New()
	err := run.Clients.NewClients(ctx, &run.Info)
	if err != nil {
		log.Fatal("failed to init clients : ", err)
	}

	kinteract, err := kubeinteraction.NewKubernetesInteraction(run)
	if err != nil {
		log.Fatal("failed to init kinit client : ", err)
	}

	// loggerConfiguratorOpt := evadapter.WithLoggerConfiguratorConfigMapName(logging.ConfigMapName())
	// loggerConfigurator := evadapter.NewLoggerConfiguratorFromConfigMap(PACControllerLogKey, loggerConfiguratorOpt)
	// copt := evadapter.WithLoggerConfigurator(loggerConfigurator)
	// // put logger configurator to ctx
	// ctx = evadapter.WithConfiguratorOptions(ctx, []evadapter.ConfiguratorOption{copt})
	// // set up kubernetes interface to retrieve configmap with log configuration
	// ctx = context.WithValue(ctx, client.Key{}, run.Clients.Kube)

	// ctx = evadapter.WithNamespace(ctx, system.Namespace())
	// ctx = evadapter.WithConfigWatcherEnabled(ctx)

	if err := adapter.New(run, kinteract).Start(context.Background()); err != nil {
		log.Fatal("failed to start adapter : ", err)
	}
}
