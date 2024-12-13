# Copyright 2024
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM nginx:1.27
ARG K0S_VERSION
ADD default.conf /etc/nginx/conf.d/default.conf
ADD --chown=nginx https://github.com/k0sproject/k0s/releases/download/${K0S_VERSION}/k0s-${K0S_VERSION}-amd64 /var/www/html/k0s
ADD --chown=nginx https://github.com/k0sproject/k0s/releases/download/${K0S_VERSION}/k0s-airgap-bundle-${K0S_VERSION}-amd64  /var/www/html/k0s-airgap-bundle
