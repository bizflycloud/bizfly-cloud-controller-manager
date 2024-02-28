ginkgo -v --json-report=report.json -- \
  --bizfly-url="https://manage.bizflycloud.vn" \
  --auth-method="application_credential" \
  --app-cred-id="app-cred-id" \
  --app-cred-secret="app-cred-secret" \
  --use-existing=true \
  --cluster-uid="cluster-uid"