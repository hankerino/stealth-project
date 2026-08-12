# The exchange namespace + the first workload wiring.
#
# NOTE on the image: the deployment points at OUR ECR (`api:latest`). Until a
# first image is pushed (via the CI runners), the pods will ImagePullBackOff —
# expected. The Service + TargetGroupBinding still prove the edge wiring end
# to end as soon as pods exist.

resource "kubernetes_namespace" "exchange" {
  metadata {
    name = "exchange"
  }
}

resource "kubernetes_service_account" "trading_engine" {
  metadata {
    name      = "trading-engine"
    namespace = kubernetes_namespace.exchange.metadata[0].name
    annotations = {
      "eks.amazonaws.com/role-arn" = aws_iam_role.trading_engine.arn
    }
  }
}

resource "kubernetes_deployment" "trading_engine" {
  metadata {
    name      = "trading-engine"
    namespace = kubernetes_namespace.exchange.metadata[0].name
    labels    = { app = "trading-engine" }
  }

  spec {
    replicas = 2

    selector {
      match_labels = { app = "trading-engine" }
    }

    template {
      metadata {
        labels = { app = "trading-engine" }
      }

      spec {
        service_account_name = kubernetes_service_account.trading_engine.metadata[0].name

        container {
          name = "api"
          # Placeholder only. ECR tags are immutable (git-sha per build) and
          # CI sets the real image on every deploy (kubectl set image).
          image = "${var.ecr_registry}/api:bootstrap"

          port {
            name           = "https"
            container_port = 8443
          }

          resources {
            requests = { cpu = "100m", memory = "128Mi" }
            limits   = { cpu = "500m", memory = "512Mi" }
          }
        }
      }
    }
  }
}

resource "kubernetes_service" "trading_engine" {
  metadata {
    name      = "trading-engine"
    namespace = kubernetes_namespace.exchange.metadata[0].name
  }

  spec {
    selector = { app = "trading-engine" }

    port {
      name        = "https"
      port        = 443
      target_port = 8443
    }
  }
}

# Registers the service's pod IPs into the EDGE target group (created in
# dev/edge). The LB controller keeps membership current as pods churn —
# this is the edge-ALB -> workload link.
resource "kubernetes_manifest" "trading_engine_tgb" {
  manifest = {
    apiVersion = "elbv2.k8s.aws/v1beta1"
    kind       = "TargetGroupBinding"
    metadata = {
      name      = "trading-engine"
      namespace = kubernetes_namespace.exchange.metadata[0].name
    }
    spec = {
      targetGroupARN = local.edge_tg_arn
      targetType     = "ip"
      serviceRef = {
        name = kubernetes_service.trading_engine.metadata[0].name
        port = 443
      }
    }
  }

  # The AWS Load Balancer Controller is installed by the eks module — apply
  # that layer first so the TargetGroupBinding CRD exists.
}
