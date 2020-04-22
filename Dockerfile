# Copyright 2019 The BizFly Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

FROM amd64/alpine:3.11

RUN apk add --no-cache ca-certificates

ADD bizfly-cloud-controller-manager /bin/

CMD ["/bin/bizfly-cloud-controller-manager"]